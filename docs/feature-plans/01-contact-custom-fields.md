# 01 — Contact Record with Custom Fields (Contacts Module)

| | |
|---|---|
| **Priority** | **P0** |
| **Phase** | 1 |
| **Effort** | 3 engineer-weeks |
| **Depends on** | F1 migrations, F2 events, F3 activity log, F6 query engine, F7 phone normalisation |
| **Unlocks** | 02 timeline (profile page), 05 segments, 06 merge, 07 deal fields, 08 automation, 09 reporting |
| **Status** | Proposed |

## 1. Problem
- Contacts carry only `profile_name`, `tags` and a free-form `metadata` JSONB (`internal/models/models.go:345-373`).
- `metadata` has no schema, no field types, no editing UI (`ContactDetailView.vue` does not show it; `ContactInfoPanel.vue:349` renders it read-only), and cannot be filtered.
- Contacts live under **Settings → Contacts** (`router/index.ts:201-210`), which frames them as configuration rather than the core record of a CRM.

## 2. Goals
1. **Org-defined fields:** each organization defines its own contact fields of type **text, number, date, dropdown, email, phone**, stored as typed values that can be validated, sorted and filtered.
2. **Built-in fields:** every org gets **email, company, address, source, lifecycle stage**, seeded out of the box.
3. **Contacts module:** Contacts becomes a **main-menu module** with a list page that supports field columns, filters, server-side sort and bulk actions.
4. **Fields everywhere:** fields appear and are editable in the chat side panel, the contact page, create/import/export, and are available to chatbot flows, custom actions, webhooks, segments (05) and automation (08).

### Non-goals (v1)
- Multi-select and boolean field types. The schema allows them later via a new `type` value.
- Per-field permissions. Everyone with `contacts:write` can edit all fields.
- Computed or formula fields.
- Automatic migration of existing `metadata` keys. A manual "promote to field" tool is provided instead.

## 3. Current state (relevant facts)
| Area | Fact | Ref |
|---|---|---|
| Uniqueness | `UNIQUE (organization_id, phone_number)`, not partial, so soft-deleted rows keep their phone | `postgres.go:243-276` |
| Create paths | `contactutil.GetOrCreateContact` (inbound, reactions, echoes, calls, campaign worker), `CreateContact` handler, CSV import | `contactutil.go:18`, `contacts.go:1330`, `import_export.go` |
| Update | `PUT /api/contacts/{id}` replaces `metadata` wholesale | `contacts.go:1437` |
| List | search + tag OR-filter only, fixed sort, N+1 unread | `contacts.go:87-137` |
| Metadata consumers | custom actions `{{contact.metadata.x}}` (`custom_actions.go:576,600`); chatbot writes to `session_data`, not metadata | `chatbot_graph_runner.go:259` |
| Import | columns phone/profile_name/whatsapp_account/tags/assigned_user_id; dedupe on exact phone; fails on soft-deleted duplicates | `import_export.go` |
| Webhook payload | `ContactEventData` has id, phone, name only | `webhook_dispatch.go:42` |

## 4. Design

### 4.1 Data model

Definitions and values use generic `custom_field_*` tables with an `entity_type`, so deals (07) reuse the same system. Everything in this plan uses `entity_type = 'contact'`.

```sql
custom_field_definitions (
  id              uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  entity_type     varchar(20) not null default 'contact',   -- contact | deal (07)
  key             varchar(64) not null,        -- slug [a-z][a-z0-9_]*, immutable after create
  label           varchar(100) not null,
  description     varchar(500) not null default '',
  type            varchar(20) not null,        -- text | number | date | dropdown | email | phone
  options         jsonb not null default '[]', -- dropdown: [{"value":"lead","label":"Lead","color":"blue"}]
  validation      jsonb not null default '{}', -- {"max_length":255,"min":0,"max":100,"multiline":true}
  default_value   jsonb null,
  is_system       boolean not null default false,
  is_required     boolean not null default false,  -- enforced on manual create/edit only
  show_in_list    boolean not null default false,
  show_in_chat_panel boolean not null default true,
  group_label     varchar(64) not null default '',  -- UI section, e.g. "Company"
  position        integer not null default 0,
  archived_at     timestamptz null,
  created_by_id   uuid null, updated_by_id uuid null,
  created_at, updated_at, deleted_at            -- BaseModel
);
create unique index idx_cfd_org_entity_key on custom_field_definitions (organization_id, entity_type, key) where deleted_at is null;

custom_field_values (
  id              uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  entity_type     varchar(20) not null default 'contact',
  entity_id       uuid not null,               -- contacts.id (or deals.id in 07)
  field_id        uuid not null,
  value_text      text null,                   -- text, email (lower-cased), phone (normalised)
  value_number    numeric(20,6) null,
  value_date      date null,
  value_option    varchar(100) null,           -- dropdown option value
  updated_by_id   uuid null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);
create unique index idx_cfv_entity_field on custom_field_values (entity_type, entity_id, field_id);
create index idx_cfv_text   on custom_field_values (organization_id, field_id, lower(value_text)) where value_text is not null;
create index idx_cfv_number on custom_field_values (organization_id, field_id, value_number)       where value_number is not null;
create index idx_cfv_date   on custom_field_values (organization_id, field_id, value_date)         where value_date is not null;
create index idx_cfv_option on custom_field_values (organization_id, field_id, value_option)       where value_option is not null;
```

**Decisions**
- **Typed columns vs. a JSONB blob:** the requirement is "stored as typed values". Typed columns give correct sorting and range filters, and btree indexes per type. A JSONB blob would need casts in every query and cannot enforce types.
- **No foreign key on `entity_id`:** keeps the table polymorphic. Plan 06 re-points values on merge, and contact soft-delete leaves values in place so a restore keeps them.
- **Empty value = no row:** setting a field to null deletes the row.
- **`metadata` stays unchanged:** it keeps serving chatbot/custom-action integrations. The UI labels it "Additional data".

### 4.2 Built-in (system) fields
Seeded for every org by an F1 migration, and for new orgs in the organization-create handler (`organization.go`).

| key | label | type | options / notes |
|---|---|---|---|
| `email` | Email | email | case-insensitive; `show_in_list` true |
| `company` | Company | text | `show_in_list` true |
| `address` | Address | text | `validation.multiline = true`, max 1000 |
| `source` | Source | dropdown | `whatsapp_inbound`, `campaign`, `import`, `manual`, `api`, `website`, `referral`, `other` |
| `lifecycle_stage` | Lifecycle stage | dropdown | `new`, `lead`, `qualified`, `customer`, `inactive`; `default_value = "new"`; `show_in_list` true |

System field rules:
- They cannot be deleted, archived or change type or key.
- Labels, option labels/colours/order, `show_in_*` and `is_required` are editable.
- Orgs may **add** options to `source` and `lifecycle_stage`. System option values (listed above) cannot be removed, because auto-population and reports rely on them.

**Auto-population on contact creation** (only when the value is empty):

| Creation path | `source` | `lifecycle_stage` |
|---|---|---|
| Inbound message / call (`GetOrCreateContact` from webhook) | `whatsapp_inbound` | default |
| Campaign worker creates the contact | `campaign` | default |
| CSV import | `import` (unless a column supplies it) | default or column |
| `CreateContact` from UI | `manual` | default |
| `CreateContact` via API key | `api` | default |

`contactutil.GetOrCreateContact` gains a `CreateContext{Source string, Actor}` argument. Existing callers pass the correct source.

### 4.3 Backend

**Package `internal/customfields`**
- `Definitions(db, orgID, entityType)` is cached in Redis per org+entity and invalidated on any definition write.
- `Validate(def, raw any) (TypedValue, error)` handles per-type parsing:
  - email: trim, lower, regex, ≤ 254 chars
  - phone: `phoneutil.Normalize` with the org default country
  - number: parse, respect min/max
  - date: `YYYY-MM-DD`
  - dropdown: value must exist in options
  - text: `max_length`, default 1000
- `SetValues(tx, orgID, entityType, entityID, map[string]any, actor) (changes []FieldChange, err error)` upserts or deletes rows and returns the diffs.
- `LoadValues(db, entityType, ids []uuid.UUID) map[uuid.UUID]map[string]any` loads a whole page with one query.
- It registers `field.<key>` entries in the F6 `contactquery.Registry` at startup, lazily per org (the registry resolves definitions per request).

**Events** (F2, published with `PublishTx`)
- `contact.field_changed` per changed key: `data = {key, label, type, from, to}`.
- `contact.lifecycle_stage_changed` when `lifecycle_stage` changes: `data = {from, to}`. It is recorded in activity and used by 09's funnel.
- One `contact.updated` per request (webhook), carrying the changed keys.

**Routes** (in `setupRoutes`)

| Method & path | Handler | Permission |
|---|---|---|
| `GET /api/custom-fields?entity_type=contact&include_archived=` | `ListCustomFields` | `contact_fields:read` **or** `contacts:read` (read-only rendering) |
| `POST /api/custom-fields` | `CreateCustomField` | `contact_fields:write` |
| `PUT /api/custom-fields/{id}` | `UpdateCustomField` | `contact_fields:write` |
| `PUT /api/custom-fields/reorder` | `ReorderCustomFields` body `{ids:[...]}` | `contact_fields:write` |
| `POST /api/custom-fields/{id}/archive` / `…/unarchive` | | `contact_fields:write` |
| `DELETE /api/custom-fields/{id}` | only if no values exist, otherwise 409 with count | `contact_fields:delete` |
| `POST /api/custom-fields/{id}/options/remap` body `{from, to}` | re-maps dropdown values before removing an option | `contact_fields:write` |
| `PATCH /api/contacts/{id}/fields` body `{fields:{company:"Acme", email:null}}` | `UpdateContactFields` (partial) | `contacts:write` + contact access scope |
| `POST /api/contacts/search` | F6 list v2, `include: ["fields"]` | `contacts:read` (scoped) |
| `POST /api/contacts/bulk` body `{contact_ids? , filter?, action, payload}` | `BulkUpdateContacts` | `contacts:write` (`delete` needs `contacts:delete`) |
| `POST /api/contacts/metadata/promote` body `{metadata_key, field:{…}}` | copies values into a new field | `contact_fields:write` |

- **Bulk actions:** `set_field`, `add_tags`, `remove_tags`, `assign`, `delete`. Up to 1,000 contacts are processed synchronously in batches of 200. Larger selections return 422 `too_many_contacts`; background bulk jobs arrive with 05, which can target a segment.
- **Type changes:** allowed only when the field has no values (409 otherwise). Removing a dropdown option that is in use requires a remap first (409 with usage count).
- **`GET /api/contacts/{id}` and `PUT /api/contacts/{id}`:** the response gains `fields` (key → value), `phone_normalized` and `marketing_opt_out`. `PUT` keeps accepting `metadata` but gains a `metadata_merge: true` option that merges instead of replacing.
- **Contact create** (`POST /api/contacts`) accepts `fields`, enforces `is_required` and applies defaults.

**Import / export** (`internal/handlers/import_export.go`)
- **Columns:** export supports `field.<key>` columns, `marketing_opt_out` and `lifecycle_stage` (alias of `field.lifecycle_stage`). The import template download lists all active fields.
- **Matching:** uses `phoneutil` (F7). The order is exact `phone_number`, then `phone_normalized`, then soft-deleted rows, which are **restored** instead of failing on the unique index.
- **Validation:** invalid field values reject that row with a per-row error (row number, column, reason). Other rows continue.
- **Created contacts:** receive `source=import` unless the column is present, and publish `contact.created` in batches.
- **Audit:** the import writes one `audit_logs` entry (`resource_type='contact_import'`) with counts.

**Integrations**
- **Chatbot:**
  - `prompt` and `api_call` nodes gain an optional `save_to_field` (prompt) / `field_mapping` (api_call `response_mapping` target `contact.fields.<key>`) config.
  - Values are validated and written through `customfields.SetValues` with `actor_type=system`. Invalid values are logged and not written. The flow continues because prompt validation already guards user input.
  - Session data is still written as today.
- **Custom actions:** `buildActionContext` (`custom_actions.go:576`) adds `contact.fields` (key → display value).
- **Webhooks:** `ContactEventData` gains `tags`, `fields`, `lifecycle_stage`, `assigned_user_id` for `contact.*` events. `message.*` payloads are unchanged.
- **Tags:** unchanged. Tag add/remove events are instrumented in F3.

### 4.4 Frontend

**Navigation and routing**
- `navigation.ts` Main section: add `{ name: 'nav.contacts', path: '/contacts', icon: Contact, permission: 'contacts' }` after Chat, and add `contacts` to the section's `permissions`.
- Settings children: remove `nav.contacts`; add `{ name: 'nav.contactFields', path: '/settings/contact-fields', icon: SlidersHorizontal, permission: 'contact_fields' }`.
- `router/index.ts`:
  - Routes: `/contacts` → `views/contacts/ContactsListView.vue`; `/contacts/:id` → `views/contacts/ContactProfileView.vue` (Phase 1: current detail view enhanced; replaced by the timeline profile in 02); `/settings/contact-fields` → `views/settings/ContactFieldsView.vue`.
  - Redirects: `/settings/contacts` → `/contacts` and `/settings/contacts/:id` → `/contacts/:id`, so existing bookmarks and e2e tests still work.
  - Update `navigationOrder`.

**Contacts list** (`views/contacts/ContactsListView.vue`)
- **Header:** title "Contacts" with a total count. Actions: Import/Export (existing `ImportExportDialog`), Add contact.
- **Toolbar:**
  - Search.
  - Quick filters: Lifecycle stage (multi-select), Assigned to (user picker + "Me" + "Unassigned"), Tags, WhatsApp account.
  - "Filters" button opening `FilterBuilder` (F6) for any field.
  - Active filter chips with remove.
  - Columns menu.
- **Table:** extend `components/shared/DataTable.vue` with `selectable` + `v-model:selected` (row checkboxes, header select-page) and `serverSort` (emit sort instead of local sort).
  - Default columns: Name (+ last message preview), Phone, Lifecycle stage (badge with option colour), Company, Email, Tags, Assigned, Last message, Created.
  - Field columns come from `show_in_list`; per-user visible columns are stored in `users.settings.contacts_list.columns`.
- **Bulk bar** (appears when rows are selected): Set field…, Add tags, Remove tags, Assign, Delete. Each action opens a small dialog that reuses `FieldInput`.
- **Views slot:** a dropdown placeholder "All contacts", filled by segments in 05.
- **State:** the filter AST, sort and page live in the URL query (`?f=<base64url JSON>&sort=`), so links are shareable and back/forward works.

**Contact fields settings** (`views/settings/ContactFieldsView.vue`)
- Sortable list (vuedraggable, already a dependency) showing label, key, type icon, System badge, and "in list" / "in chat" toggles.
- Create/edit dialog:
  - label (auto-suggests key)
  - type (disabled after values exist)
  - description, group
  - type-specific editor: options editor with colours for dropdown; min/max for number; max length and multiline for text
  - required, default value
- Archived fields sit in a collapsible section. Removing an in-use dropdown option triggers the remap dialog.

**Reusable components** (`components/contacts/fields/`)
- `FieldInput.vue` (by type; phone input uses the org default country hint) and `FieldValue.vue` (display: mailto/tel links, badge for dropdown, localised number/date).
- `ContactFieldsForm.vue` renders definitions grouped by `group_label`, with dirty tracking.
- `stores/customFields.ts` caches definitions per entity type and refetches on WS `custom_fields_updated` (new org-wide event).

**Chat side panel** (`components/chat/ContactInfoPanel.vue`)
- New **Details** section between Tags and Metadata:
  - fields with `show_in_chat_panel`, click-to-edit inline (`contacts:write`), saved via `PATCH /fields`, with an optimistic update and rollback on error
  - lifecycle stage shown as a coloured select at the top
- The existing metadata section is renamed "Additional data" and starts collapsed.
- The panel's hardcoded English strings move to i18n while touching the file.

**Create contact dialog** (`components/shared/CreateContactDialog.vue`): adds email, company, lifecycle stage and any `is_required` fields.

**i18n:** new `contacts.fields.*`, `contactFields.*`, `nav.contacts`, `nav.contactFields` keys in `en.json`.

## 5. Migration & rollout
1. **Schema:** AutoMigrate `CustomFieldDefinition`, `CustomFieldValue`; add indexes to `getIndexes`; add models to `testutil` `runMigrations`.
2. **F1 migrations:**
   - `…_custom_fields_seed_system` for all orgs.
   - `…_permissions_contact_fields`: admin all; manager read/write; agent read.
   - Optional `…_contacts_source_backfill`, a heuristic: a contact whose earliest message is incoming gets `whatsapp_inbound`; if the earliest touch is a campaign recipient, `campaign`; otherwise leave empty. It only sets empty values.
3. **Deploy:**
   - Backend endpoints first; the old `GET /api/contacts` keeps working.
   - Then the frontend: new module plus redirects.
   - Nothing is removed from `metadata`.
4. **Feature flag:** not required; the change is additive.

## 6. Milestones
| # | Deliverable | Est. |
|---|---|---|
| M1 | Models, indexes, seed migration, `customfields` package + unit tests | 3 d |
| M2 | Definition CRUD API + contact fields PATCH + events + audit | 2 d |
| M3 | F6 registry integration (`field.<key>`), `/contacts/search`, bulk endpoint | 2 d |
| M4 | Import/export with fields and F7 matching fixes | 2 d |
| M5 | Contact fields settings UI | 2 d |
| M6 | Contacts module list (DataTable selection/server sort, filters, columns, bulk) + routing/nav | 3 d |
| M7 | Chat panel Details section, create dialog, chatbot `save_to_field`, custom action + webhook payloads | 2 d |

## 7. Testing
- **Unit:** `customfields.Validate` for every type and edge case (email case, phone with trunk 0, number bounds, invalid date, unknown option).
- **Handler tests:**
  - definition CRUD, including system-field protection, type change blocked with values, and option removal blocked until remap
  - `PATCH /fields` partial update, null deletes, access scope (an agent without `contacts:read` cannot edit an unassigned contact)
  - events published (activity rows created, lifecycle event emitted once)
  - import with fields, a soft-deleted duplicate restored, `+`/trunk variants matched
- **Query tests:** `field.company contains`, `field.lifecycle_stage in`, date `within_last` in org timezone, sort by a number field.
- **E2E** (`e2e/tests/contacts/*`, page objects `ContactsListPage`, `ContactFieldsPage`):
  - create a dropdown field, set it from the chat panel, filter the list by it, bulk-assign
  - old `/settings/contacts` URL redirects

## 8. Risks & open questions
| Risk / question | Mitigation / proposal |
|---|---|
| EAV filter performance on large orgs (≥ 1M values) | Per-type partial indexes, EXISTS subqueries, `EXPLAIN` tests on a seeded 500k-contact DB before release |
| Contacts module duplicates chat list features | Contacts is the CRM record view (all contacts, fields, bulk); Chat stays the conversation inbox (03). They share the same components |
| Should `email` be unique per org? | Not in v1 (families share emails); used as a **duplicate signal** in 06 |
| Should agents edit lifecycle stage? | Yes by default (`contacts:write`); per-field permissions are a later enhancement |
| Required fields on inbound auto-created contacts | Not enforced (no human present); the list offers a "Missing required fields" filter via `is_empty` |

## 9. Acceptance criteria
- [ ] An admin can create, reorder, archive and edit fields of all six types, and system fields cannot be deleted or re-typed.
- [ ] Every org, including existing ones after `-migrate`, has email, company, address, source and lifecycle stage.
- [ ] New inbound contacts get `source=whatsapp_inbound` and `lifecycle_stage=new`.
- [ ] Contacts appears in the main menu; `/settings/contacts` redirects; Settings has Contact Fields.
- [ ] The contacts list filters on any field with correct type semantics, sorts server-side, and bulk-updates up to 1,000 selected contacts.
- [ ] Field edits from the chat panel and contact page create `contact.field_changed` activity rows and update other open tabs in real time.
- [ ] CSV import/export round-trips custom fields; soft-deleted duplicates are restored, not errors.
- [ ] Custom actions can use `{{contact.fields.company}}`; `contact.updated` webhooks include `fields`.


---

## Integration addendum (from [10 — System integration](10-system-integration.md))

- **Creation paths:**
  - Source auto-population lives in the **Contact Lifecycle service** (`ResolveOpts.Source`, S2), not in scattered callers.
  - CSV import uses a **dedicated contacts importer** on top of the lifecycle service. The generic reflection importer cannot map `field.<key>` and skips events and validation.
  - `source=api` / `actor_type=api` require the new `auth_method` context key (S1).
- **Chat panel data:** the chat panel must fetch `GET /contacts/{id}?include=fields,owner,conversation` when a contact is selected; list rows never carry fields (`ChatView.vue:561`). The panel's "No data configured" empty state applies only to the session-data section.
- **Variables:** one namespace, `contact.fields.<key>` everywhere (canned responses, custom actions, campaigns, automation, chatbot, AI, IVR), via `internal/render` + `internal/crmcontext` (S6). `field.<key>` remains only as the F6 **filter field** key.
- **Chatbot integration** is broader than `save_to_field` (S7):
  - `save_to_field` on prompt, **buttons** and **WhatsApp Flow** nodes
  - a separate `field_mapping` (field key to JSON path) on api_call, not inside `response_mapping`
  - the CRM action and CRM condition nodes from the shared action library
  - PanelConfig gains "Save to contact field" per session variable
- **UI placement:**
  - Details, tags, owner and fields render as **ContactSidebar sections** (S12), shared by chat and profile.
  - Contact fields settings are reached from the Contacts module header (gear) and the Settings overview, **not** a new Settings sidebar child (10 §4.8).
- **Contacts move checklist** (all must change together with the redirect):
  - `navigation.ts:132,140` (Settings `permissions` / `childPermissions`)
  - `router/index.ts:366` `navigationOrder`
  - `DashboardView.vue:133` shortcut
  - `ContactsView.vue:209` links
  - e2e `ContactsPage extends TableSettingsPage` (`e2e/pages/SettingsPage.ts:488`) and `tests/settings/contacts.spec.ts`
- **DataTable v2** (row click, selection, server sort, column visibility) is built once in Phase 0 (S12), not inside this plan.
- **Entity references:** removing a dropdown option or archiving a field goes through `entityrefs` (S8), so segments and automation configs using it are rewritten or flagged.
- **i18n:** template braces in locale strings must be escaped, or the i18n e2e validation fails.
