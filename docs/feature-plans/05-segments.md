# 05 — Segments (Saved Filters) and Campaign Targeting

| | |
|---|---|
| **Priority** | **P1** — where the CRM and WhatsApp halves connect |
| **Phase** | 2 |
| **Effort** | 2.5 engineer-weeks |
| **Depends on** | 01 (fields, Contacts module), F6 query engine + FilterBuilder, F8 recipient `contact_id`, F10 messaging service, F4 scheduler (scheduled campaigns). Optional fields from 03/04/07 appear automatically once registered in F6 |
| **Unlocks** | 08 automation (`in segment` condition, scheduled segment sends), 09 reports by segment |
| **Status** | Proposed |

## 1. Problem
- Contacts can be filtered only by search and tags.
- Campaign recipients are pasted or uploaded in the browser (`CampaignDetailView.vue:598-890`) and posted as a phone list (`POST /api/campaigns/{id}/recipients/import`, `campaigns.go:650`). Recipients have no link to contacts, and there is no dedupe or opt-out check at insert time.
- Scheduled campaigns never start (`scheduled_at` is stored but never polled).

The CRM knows who the customers are; campaigns cannot use that knowledge.

## 2. Goals
1. **Filters:** filter contacts by tags, custom fields, lifecycle stage, last message date, opt-out status, assigned agent (and conversation status, open tasks, deal stage once those plans ship). **Save the filter as a segment.**
2. **Dynamic segments:** membership is evaluated when used, so new matching contacts are included automatically.
3. **Campaign targeting:** campaigns target a segment. At send time, everyone matching (minus opted-out contacts for marketing templates) becomes a recipient. Template variables map to contact data.
4. **Segments as views:** segments appear as saved views in the Contacts module, with counts, export and "Send campaign".

### Non-goals (v1)
- Static segments (manual membership lists). A campaign snapshot covers the audit need; static lists can come later as `type='static'`.
- "Contact enters/leaves segment" triggers (owned by 08 as a later enhancement).
- Bulk actions on a whole segment beyond the 01 limit (1,000); background bulk jobs come later.

## 3. Current state
| Area | Fact | Ref |
|---|---|---|
| Query engine | F6 filter AST + registry + `POST /api/contacts/search` | 00 F6 |
| Campaign model | one template, header media, counters, `ScheduledAt` | `bulk.go:10-38` |
| Recipients | `phone_number`, `recipient_name`, `template_params`, `header_params`, status; no contact link (F8 adds `contact_id`) | `bulk.go:45-67` |
| Start | `StartCampaign` enqueues pending recipients to Redis stream | `campaigns.go:405` |
| Worker | `GetOrCreateContact`, marketing opt-out check, own send path | `worker.go:69-256` |
| Opt-out | `contacts.marketing_opt_out` set from Meta `user_preferences` webhook | `webhook.go:576` |

## 4. Design

### 4.1 Data model
```sql
segments (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  name varchar(120) not null,
  description varchar(500) not null default '',
  filter jsonb not null,                 -- F6 AST (validated on save)
  visibility varchar(10) not null default 'shared', -- shared | private
  created_by_id uuid not null,
  updated_by_id uuid null,
  contact_count integer null,            -- cached
  counted_at timestamptz null,
  last_used_at timestamptz null,         -- viewed / used by a campaign
  created_at, updated_at, deleted_at
);
create unique index idx_segments_org_name on segments (organization_id, lower(name)) where deleted_at is null;
create index idx_segments_org on segments (organization_id, visibility, created_by_id);
```

**Campaign additions** (`bulk_message_campaigns`)
```sql
audience_type   varchar(10) not null default 'list',  -- list | segment
segment_id      uuid null,
audience_filter jsonb null,          -- filter snapshot captured at materialisation (audit)
param_mappings  jsonb not null default '{}',
audience_count  integer null,         -- recipients created at materialisation
excluded_count  integer null,         -- skipped (opt-out, no valid phone, duplicates)
materialized_at timestamptz null
```

`param_mappings` shape (shared with 08 `send_template`):
```json
{
  "body":    { "1": { "source": "contact.name", "fallback": "there" },
               "2": { "source": "field.company" } },
  "header":  { "1": { "source": "static", "value": "Autumn sale" } },
  "buttons": { "0": { "source": "field.coupon_code" } }
}
```
**Sources:** `contact.name`, `contact.phone_number`, `field.<key>` (display value), `static`. `fallback` is used when the value is empty. If there is no fallback and the value is required, that recipient is **excluded** and counted.

### 4.2 Segment semantics
- **Filter AST:** exactly F6 (all registered fields, including `field.*`, `conversation.*`, `task.*`, `deal.*` as they ship).
- **Nesting:** rule `{ "field": "segment", "operator": "in" | "not_in", "value": "<segment_id>" }` compiles the referenced segment's filter inline. Depth is ≤ 3; cycles are rejected on save (DFS over referenced IDs); deleting a referenced segment is blocked (409 listing dependents).
- **Viewer scoping:** segment membership **ignores** the viewer's contact scope when used by campaigns (campaign permission governs) but **applies** it when browsing in the Contacts module. An agent browsing "VIP" sees only contacts they can access.
- **Counts:**
  - `POST /api/segments/{id}/count` runs a count with `SET LOCAL statement_timeout = '5s'`; on timeout it returns `{count: null, estimated: true}` and the job refreshes it later.
  - Scheduler job `segment_count_refresh` (F4, 15 min) recounts segments with `last_used_at` in the last 7 days or attached to a draft/scheduled campaign, and broadcasts `segment_count_updated`.

### 4.3 Backend
**Package** `internal/segments` (service, cycle checks, materialisation) + `internal/handlers/segments.go`.

| Method & path | Purpose | Permission |
|---|---|---|
| `GET /api/segments?search=&visibility=` | List (private segments only for the creator) | `segments:read` |
| `POST /api/segments` `{name, description, filter, visibility}` | Create (validates AST, cycles) | `segments:write` |
| `GET/PUT/DELETE /api/segments/{id}` | CRUD; delete blocked if used by draft/scheduled campaigns or other segments | `segments:read/write/delete` |
| `POST /api/segments/preview-count` `{filter}` | Count for an unsaved filter (FilterBuilder live count, debounced) | `contacts:read` |
| `POST /api/segments/{id}/count` | Recount now | `segments:read` |
| `POST /api/segments/{id}/contacts` `{sort, page, limit, include}` | Members (same shape as `/api/contacts/search`), viewer-scoped | `segments:read` + `contacts:read` |
| `POST /api/segments/{id}/export` | CSV export of members (reuses import_export column logic incl. `field.*`) | `segments:read` + `contacts:export` |
| `PUT /api/campaigns/{id}/audience` `{audience_type, segment_id?, param_mappings}` | Set audience on a draft campaign | `campaigns:write` + `segments:read` |
| `POST /api/campaigns/{id}/audience/preview` | `{count, excluded:{opt_out, invalid_phone, missing_params}, sample:[{contact, rendered_body}]}` (10 samples) | `campaigns:read` |

**Materialisation** (`segments.MaterializeCampaignAudience(ctx, campaignID)`)
1. **When:** called by `StartCampaign` (manual start) and by the F4 `scheduled_campaigns` job (fixes the scheduled defect) for `audience_type='segment'` campaigns whose `materialized_at` is null.
2. **Lock:** takes the campaign row with `FOR UPDATE`; aborts if already materialised (idempotent).
3. **Select:** compiles the segment filter **without** viewer scope and streams contacts in keyset batches of 1,000 (`ORDER BY id`).
4. **Per contact:**
   - skip group JIDs / empty phone (`invalid_phone`)
   - skip `marketing_opt_out` when the template category is `MARKETING` (`opt_out`)
   - resolve `param_mappings` via `messaging.ResolveParams(contact, fieldValues, mappings)` (F10); skip on missing required values (`missing_params`)
   - insert `bulk_message_recipients` with `contact_id`, `phone_number`, `recipient_name`, `template_params`, `header_params`, `status='pending'`; a unique partial index `(campaign_id, contact_id)` prevents duplicates
5. **Finish:** writes `audience_filter` (snapshot), `audience_count`, `excluded_count`, `materialized_at`, then continues with the existing enqueue path.
6. **Guardrails:** org setting `campaigns.max_recipients` (default 100,000). Exceeding it aborts with a clear error and the campaign stays draft.

**Worker:** unchanged except it uses the F10 messaging service. `ButtonURLParams` now come from recipient data, fixing the missing button params.

**List audiences:** `audience_type='list'` keeps the current CSV/manual import untouched. F8 links recipients to contacts when sent.

### 4.4 Frontend
**Contacts module views** (`views/contacts/ContactsListView.vue`, from 01)
- **Views sidebar** (collapsible on desktop, dropdown on mobile): **All contacts**, **My segments**, **Shared segments**, each segment with its cached count, plus "+ New segment".
- **Filter bar** actions:
  - on an unsaved filter: "Save as segment" (name, description, visibility)
  - on a segment: "Update segment" / "Save as new"
- **Segment header** (when a segment is selected): name, description, count ("≈" when estimated) with refresh, "Used in 2 campaigns", buttons **Send campaign** (creates a draft campaign with this audience and navigates to it), **Export**, **Edit**, **Delete**.
- **FilterBuilder** (F6) shows a live match count (debounced 500 ms via `preview-count`).
- The URL reflects `?segment=<id>` or `?f=<ast>`.

**Campaign detail** (`views/settings/CampaignDetailView.vue`)
- **Audience tab** replaces the Recipients tab title, with a radio choice:
  - **Segment:** segment picker (with counts), "Edit filter" link, or "Build new segment" (FilterBuilder in a dialog → save).
  - **Upload list:** the existing manual/CSV UI, unchanged.
- **Personalisation** section: for each template variable (`{{1}}`, header, URL buttons) a source select (Contact name, Phone, any contact field, Static text) + fallback input, with a live preview of three sample recipients (`audience/preview`).
- **Summary before start:** "4,812 contacts match · 213 excluded (opted out 190, missing company 23)" and a confirm dialog repeating the numbers.
- **After start:** recipients table shows contact names linking to profiles (F8 `contact_id`).
- **Scheduled:** the start time picker stays; the scheduler actually starts the campaign now. The audience is resolved at start time (UI copy says so).

## 5. Migration & rollout
1. Schema (segments table, campaign columns, recipient unique partial index `(campaign_id, contact_id) where contact_id is not null`).
2. F1 permissions `segments` (admin/manager all; agent read).
3. The `scheduled_campaigns` F4 job ships **with** this plan if not already delivered (it covers both list and segment audiences).
4. The frontend feature is additive; existing list campaigns are unaffected.

## 6. Milestones
| # | Deliverable | Est. |
|---|---|---|
| M1 | Segment model, service (validation, cycles, nesting compile), CRUD + count endpoints + tests | 2.5 d |
| M2 | Materialisation + param resolution + exclusions + preview endpoint + scheduled start | 3 d |
| M3 | Contacts module views sidebar, save/update segment, live counts, export | 2 d |
| M4 | Campaign Audience tab, personalisation mapping UI, preview, confirm summary | 3 d |
| M5 | Count refresh job + WS + perf checks on 500k contacts | 1 d |

## 7. Testing
- **AST:** nesting depth limit, cycle rejection, deleting a referenced segment blocked.
- **Materialisation:**
  - opt-out excluded only for MARKETING templates
  - duplicates impossible (unique index)
  - re-invocation idempotent
  - missing params excluded vs fallback used
  - contacts created after materialisation are **not** added (snapshot semantics)
- **Scheduled:** a campaign with `scheduled_at` in the past starts once across two schedulers.
- **Scope:** an agent browsing a shared segment sees only accessible contacts; a campaign materialises all matches.
- **Performance:** a count on a 500k-contact org with 3 field rules + tags < 2 s (indexes from 01/F6).
- **E2E** (`e2e/tests/contacts/segments.spec.ts`, `e2e/tests/campaigns/segment-audience.spec.ts`):
  - build filter → save segment → Send campaign → map `{{1}}` to contact name → preview shows names → start → recipients link to contacts

## 8. Risks & open questions
| Risk / question | Mitigation / proposal |
|---|---|
| Accidental mass sends | Confirm dialog with counts; `max_recipients` guard; audience snapshot stored for audit |
| Heavy count queries | Statement timeout, cached counts, refresh job limited to recently used segments |
| Dynamic vs snapshot confusion for scheduled campaigns | Copy in UI: "Audience is calculated when the campaign starts"; show `materialized_at` after start |
| Should agents create segments? | Default read-only for agents; admins can grant `segments:write` |
| Opt-out for UTILITY templates? | Meta allows utility sends to opted-out marketing users; follow the existing worker rule (marketing only) |

## 9. Acceptance criteria
- [ ] Users can build a filter on tags, custom fields, lifecycle stage, last message/inbound dates, opt-out and assignee, see a live count, and save it as a shared or private segment.
- [ ] Segments appear as views in the Contacts module with counts, export and Send campaign.
- [ ] A campaign can target a segment; starting it (manually or at `scheduled_at`) materialises recipients linked to contacts, excluding opted-out contacts for marketing templates, with exclusion counts shown.
- [ ] Template variables can be mapped to contact name/phone/fields/static text with fallbacks, and previews render real sample recipients.
- [ ] Scheduled campaigns (list or segment) start automatically once, across replicas.
- [ ] Segments referenced by other segments or pending campaigns cannot be deleted.


---

## Integration addendum (from [10 — System integration](10-system-integration.md))

- **`param_mappings` shape (correction, S6):**
  - Keys are the template's **parameter names** (`ExtParamNames`): positional templates use `"1"`, `"2"`…, named templates use names such as `customer_name`.
  - `header`, `body` and `buttons` are separate sections, matching recipients' `template_params` / `header_params` and the worker's name-then-position lookup.
  - Sources use the shared namespace: `contact.name`, `contact.phone_number`, `contact.fields.<key>`, `static`.
- **Recipients:**
  - The unique index is `(campaign_id, contact_id) WHERE contact_id IS NOT NULL AND deleted_at IS NULL`.
  - The worker treats conflicts as permanent "duplicate" failures so campaigns complete.
  - The recipients table uses `ContactChip` and server pagination.
- **Worker:** sends through `messaging.Service` (`sender_type=campaign`), sets `recipient.message_id`, and never restores deleted or merged contacts (S2/S4).
- **Campaign reply attribution** (10 §4.5):
  - `conversations.origin_campaign_id` within a 72 h window
  - `campaign.replied` event
  - "Replied" counter
  - F6 fields `last_campaign_id` / `replied_to_campaign`
  - `source=campaign` on first touch
- **Template dependency guard** (S8):
  - Unapproval, parameter-name changes or deletion pause dependent scheduled/draft campaigns and notify owners.
  - `StartCampaign` validates approval.
  - The template picker filters by account and approval, fixing the `account` vs `whatsapp_account` parameter mismatch.
- **Rename safety:** tag renames and field option changes rewrite segment ASTs through `entityrefs`. Account filters store account IDs.
- **Frequency cap:** optional org-wide cap for marketing templates across campaigns.
- **Shared UI:** FilterBuilder and saved views are the shared components (S12). "Save as segment" is the contacts flavour of `useListViewState`.
