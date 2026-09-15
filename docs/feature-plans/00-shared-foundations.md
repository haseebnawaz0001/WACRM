# 00 — Shared Foundations

| | |
|---|---|
| **Priority** | P0 — built incrementally; each item ships with (or just before) its first consumer |
| **Phase** | 1 → 4 |
| **Effort** | ~5 engineer-weeks in total |
| **Consumers** | All feature plans (01–09) |
| **Status** | Proposed |

These are the platform pieces that several features need. Building them once avoids nine slightly different activity logs, schedulers and filter languages.

| ID | Foundation | Needed first by | Effort |
|---|---|---|---|
| F1 | Versioned run-once migrations + permission backfill | 01 (Phase 1) | 2 d |
| F2 | Domain event bus (`internal/crmevents`) | 01, 03 | 4 d |
| F3 | Contact activity log (`contact_activities`) | 01, 02 | 3 d |
| F4 | Scheduler with leader lock (`internal/scheduler`) | 03 | 3 d |
| F5 | In-app notifications | 03, 04 | 4 d |
| F6 | Contact query engine (filter AST) + contacts list v2 | 01, 05 | 5 d |
| F7 | Phone normalisation | 01 (import), 06 | 2 d |
| F8 | `contact_id` on campaign recipients | 02, 05 | 1 d |
| F9 | Organisation and user timezone | 03, 04 | 1 d |
| F10 | Messaging service extraction | 05, 08 | 3 d |
| F11 | Webhook hardening (signing bug, new events, delivery log) | fix-first, 08 | 2 d |

---

## F1 — Versioned run-once migrations and permission backfill

### Current state
- Schema changes run only through `AutoMigrate` over `GetMigrationModels()` plus idempotent SQL in `getIndexes()` (`internal/database/postgres.go:56-289`), executed when the server starts with `-migrate`.
- Data backfills are hand-written idempotent functions (e.g. `BackfillLastInboundAt`, `postgres.go:392`).
- `SeedSystemRolesForAllOrgs` only seeds orgs **without** system roles, and `FixSystemRolePermissions` only fills roles with **zero** permissions. New resources (tasks, deals, …) therefore never reach existing admin/manager/agent roles.

### Design
- New table `schema_migrations (name text primary key, applied_at timestamptz not null)`.
- New package `internal/migrations`:
  ```go
  type Migration struct {
      Name string            // "2026_10_01_contact_fields_seed" — sortable, unique
      Run  func(tx *gorm.DB) error
  }
  func Register(m Migration)
  func RunPending(db *gorm.DB, log logf.Logger) error // each migration in its own transaction
  ```
- `RunMigrationWithProgress` calls `migrations.RunPending` after AutoMigrate, indexes and seeding.
- Helper `migrations.EnsureSystemRolePermissions(tx, map[string][]string)`: for every org, inserts missing `role_permissions` rows for system roles named `admin`, `manager`, `agent`. It is idempotent (insert … where not exists). After running it clears the Redis permission cache keys and broadcasts the existing `permissions_updated` WS event per org.
- Rule for all plans: **schema** stays in AutoMigrate/getIndexes; **data** changes (seeds, backfills, permission grants) become named migrations.
- `test/testutil/db.go` runs `migrations.RunPending` after its model list so tests see seeded data.

### Acceptance
- Running `-migrate` twice applies each named migration once.
- An existing org's admin role gains a newly added permission without manual SQL.

---

## F2 — Domain event bus (`internal/crmevents`)

### Current state
- Side effects are called inline at each mutation: `a.WSHub.BroadcastTo…`, `a.DispatchWebhook(...)`, `a.logAudit(...)`. Many mutations do none of these (tag updates, contact assign).
- The WS hub is in-memory per process. Only campaign stats use Redis pub/sub (`queue/pubsub.go`). The standalone worker has no `App` and cannot reach WS clients.

### Design
```go
package crmevents

type Actor struct { Type string /* user|system|automation|contact|api */; ID *uuid.UUID; Name string }
type Subject struct { Type string /* contact|conversation|task|deal|tag|field|transfer */; ID *uuid.UUID }
type Origin struct { AutomationRuleID *uuid.UUID; Depth int } // loop protection for 08

type Event struct {
    ID         uuid.UUID
    Type       string      // catalog name, e.g. "contact.tag_added"
    OrgID      uuid.UUID
    ContactID  *uuid.UUID
    Subject    Subject
    Actor      Actor
    Data       map[string]any
    OccurredAt time.Time
    Origin     Origin
}

type Sink interface { Handle(ctx context.Context, e Event) error }

type Bus struct { /* ordered sinks */ }
func (b *Bus) Publish(ctx context.Context, e Event)            // after-commit use
func (b *Bus) PublishTx(tx *gorm.DB, e Event) (afterCommit func()) // writes activity in tx, returns fan-out closure
```

Sinks, in order:
1. **ActivitySink** (F3): writes `contact_activities` for event types flagged `recordActivity` in the catalog. Uses the caller's transaction in `PublishTx`.
2. **RealtimeSink**: maps events to WS messages (`contact_updated`, `conversation_updated`, …). It publishes to Redis channel `wacrm:ws_fanout`. Every server process subscribes and relays to its local hub (same pattern as `StartCampaignStatsSubscriber`, `app.go:166`). This fixes multi-replica delivery and lets the worker emit realtime events.
3. **WebhookSink**: calls `DispatchWebhook` when the event type is in `AvailableWebhookEvents` and the org has a subscribed webhook.
4. **StreamSink**: `XADD wacrm:crm_events MAXLEN ~ 200000` for event types flagged `automatable`. Skipped when the org has no enabled automation rules (cached flag in Redis, invalidated by 08).

- The catalog (`crmevents/catalog.go`) is the table in [README → Domain events](README.md#domain-events-f2-catalog). Each entry declares `recordActivity`, `webhook`, `automatable`, and a payload struct name.
- The bus is constructed in `main.go` and injected into `App` and `worker.New`. It must not import `handlers`.
- Existing inline `DispatchWebhook` calls for `transfer.*`, `contact.created` and `message.*` migrate to the bus gradually. Behaviour stays identical; migrating is tracked per plan.

### Acceptance
- Publishing `contact.tag_added` from a handler writes one activity row, sends one WS message to every replica's clients, and dispatches the webhook when subscribed.
- Events published from the worker reach browser clients.

---

## F3 — Contact activity log (`contact_activities`)

### Why a new table and not `audit_logs`
`audit_logs` is polymorphic (`resource_type`, `resource_id`) with **no `contact_id`**, stores field diffs for admin auditing, and is written in a goroutine (can be lost). Activity on child records (tags, tasks, deals, transfers) cannot be attributed to a contact. `audit_logs` stays as the admin audit trail; `contact_activities` is the customer-facing history.

### Data model
```sql
contact_activities (
  id               uuid primary key default gen_random_uuid(),
  organization_id  uuid not null,
  contact_id       uuid not null,
  type             varchar(64) not null,   -- F2 catalog name
  actor_type       varchar(20) not null,   -- user|system|automation|contact|api
  actor_id         uuid null,
  actor_name       varchar(255) not null default '',
  subject_type     varchar(32) null,
  subject_id       uuid null,
  data             jsonb not null default '{}', -- e.g. {"tag":"VIP"} or {"from":"lead","to":"customer"}
  occurred_at      timestamptz not null,
  created_at       timestamptz not null default now()
);
create index idx_contact_activities_timeline on contact_activities (organization_id, contact_id, occurred_at desc, id desc);
create index idx_contact_activities_type on contact_activities (organization_id, type, occurred_at desc);
```
No soft delete. Rows are immutable. There is no retention limit in v1.

### What is recorded here vs. read from source tables
- **Recorded** (state changes with no history elsewhere): tag added/removed, assignment changed, field changed, lifecycle stage changed, conversation status/assignment changes, transfer created/assigned/resumed/expired, task created/completed/cancelled, deal created/stage changed/won/lost, merge, opt-out changed.
- **Not copied** (high-volume, already have a table with timestamps): messages, call logs, conversation notes, chatbot sessions, campaign sends. The timeline (02) reads those directly.

### Instrumentation of existing code (Phase 1)
| Mutation | File | Event |
|---|---|---|
| `UpdateContactTags` | `contacts.go:1260` | diff old/new → `contact.tag_added` / `contact.tag_removed` (also add `logAudit`) |
| `AssignContact` | `contacts.go:1107` | `contact.assigned` (also add `logAudit`) |
| `UpdateContact` | `contacts.go:1437` | `contact.updated`; tag/assignee diffs as above |
| Tag rename/delete | `tags.go:162-262` | `contact.tag_removed` (+ `contact.tag_added` on rename) per affected contact, in batches |
| Transfer create/assign/resume | `agent_transfers.go:545,668,821,1181` | `transfer.*` |
| SLA auto-close | `sla_processor.go:100` | `transfer.expired` (new) |
| Marketing opt-out webhook | `webhook.go:576` | `contact.opt_out_changed` |

### Backfill (F1 migration `…_contact_activities_backfill`)
- From `audit_logs` where `resource_type='contact'`: created/updated rows.
- From `agent_transfers`: `transfer.created` at `transferred_at`, `transfer.resumed` at `resumed_at`.
- Backfill is marked `actor_type='system'` when the user is unknown.

### Read API (used by 02)
`activity.List(db, orgID, contactID, cursor, types, limit)` with keyset pagination on `(occurred_at, id)`.

---

## F4 — Scheduler with leader lock (`internal/scheduler`)

### Current state
The only periodic work is `SLAProcessor` (`sla_processor.go:13-56`), a `time.Ticker` started in the server process. It has no lock, so every replica runs it, and it iterates once per `chatbot_settings` row, so orgs with several rows are processed repeatedly. Scheduled campaigns are never started.

### Design
```go
type Job struct {
    Name     string
    Interval time.Duration
    Timeout  time.Duration
    Run      func(ctx context.Context) error
}
func (s *Scheduler) Register(j Job)
func (s *Scheduler) Start(ctx context.Context)
```
- Before each run: `SET wacrm:lock:job:<name> <instance-id> NX PX <timeout>`. Skip if not acquired; release with a compare-and-delete Lua script.
- Records `wacrm:job:<name>:last_run` / `:last_error` in Redis. The admin UI later reads these (out of scope).
- Started in the server process. Jobs must be idempotent and safe to skip one tick.
- Registered jobs (owner plan):

| Job | Interval | Owner |
|---|---|---|
| `sla_processor` (existing logic moved, de-duplicated per org) | 1 min | F4 / 03 |
| `scheduled_campaigns` (start campaigns whose `scheduled_at <= now`) | 1 min | F4 (fixes defect) / 05 |
| `conversation_snooze_wakeup` | 1 min | 03 |
| `task_due_notifier` | 1 min | 04 |
| `segment_count_refresh` | 15 min | 05 |
| `duplicate_scanner` | 1 h | 06 |
| `automation_time_triggers` | 5 min | 08 |
| `automation_runs_retention` | 24 h | 08 |
| `deal_rotting` (+ board position rebalance) | 1 h | 07 |
| `webhook_deliveries_retention` | 24 h | F11 |
| `notifications_retention` | 24 h | F5 |

---

## F5 — In-app notifications

### Current state
Only `vue-sonner` toasts plus `/notification.mp3`, fired from WS handlers (`websocket.ts:10-42`). There is nothing persisted, no bell, no browser Notification API, and no SMTP. `users.settings.email_notifications` is stored but never read.

### Data model
```sql
notifications (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  user_id uuid not null,
  type varchar(50) not null,     -- task_due, task_overdue, task_assigned, conversation_assigned,
                                 -- conversation_snooze_ended, sla_escalation, automation,
                                 -- merge_suggestions, deal_rotting
  title varchar(255) not null,
  body text not null default '',
  link varchar(500) not null default '', -- frontend route, e.g. /contacts/<id>?tab=tasks
  entity_type varchar(32), entity_id uuid,
  data jsonb not null default '{}',
  read_at timestamptz null,
  created_at timestamptz not null default now()
);
create index idx_notifications_user on notifications (user_id, organization_id, read_at, created_at desc);
```

### Backend
- `notify.Send(ctx, notify.Input{OrgID, UserIDs, Type, Title, Body, Link, Entity, Data})` inserts rows, publishes WS `notification_created` to each user, and respects per-user preferences in `users.settings.notifications[type] = {in_app: bool, sound: bool}` (default on).
- Routes:
  - `GET /api/notifications?unread_only=&cursor=&limit=`
  - `GET /api/notifications/unread-count`
  - `POST /api/notifications/{id}/read`
  - `POST /api/notifications/read-all`
- There is no permission resource: a user can only read their own notifications.
- The existing SLA escalation toast (`sla_processor.go:270`) also calls `notify.Send`.

### Frontend
- `stores/notifications.ts` holds the unread count and a paginated list, and handles `notification_created`.
- `components/layout/NotificationBell.vue`: a bell with an unread badge in the sidebar logo row (collapsed rail: icon with tooltip; mobile: top bar). A popover lists notifications and supports "Mark all read"; clicking an item navigates to `link`.
- Toast + sound behaviour is preserved and driven by the same preference.
- Profile page gains a "Notifications" section (per-type in-app and sound toggles).

### Out of scope
Email, push and browser notifications. The design keeps `notify.Send` as the single entry point so channels can be added later.

---

## F6 — Contact query engine and contacts list v2

### Current state
- `GET /api/contacts` supports only `search` and a tag OR-filter (`contacts.go:87-137`).
- Sort is fixed (`last_message_at desc`) and re-sorted client-side.
- Unread count is computed per row (N+1).
- There is no way to filter by assignee, account, dates, opt-out or custom fields.

### Filter AST (shared by 01, 03, 05, 08, 09)
```json
{
  "op": "and",
  "rules": [
    { "field": "tags", "operator": "contains_any", "value": ["VIP", "Lead"] },
    { "field": "field.lifecycle_stage", "operator": "in", "value": ["lead", "qualified"] },
    { "field": "last_inbound_at", "operator": "within_last", "value": { "amount": 30, "unit": "days" } },
    { "op": "or", "rules": [
      { "field": "assigned_user_id", "operator": "is_me" },
      { "field": "assigned_user_id", "operator": "is_unassigned" }
    ]}
  ]
}
```
- Limits: nesting depth ≤ 3, ≤ 50 rules, string values ≤ 500 chars, list values ≤ 200 items.
- **Field registry** (`contactquery.Registry`). Each field declares type, allowed operators and a SQL builder. Plans register their own fields:

| Field key | Type | Registered by |
|---|---|---|
| `phone_number`, `profile_name`, `whatsapp_account` | text / option | F6 |
| `tags` | tags | F6 |
| `assigned_user_id` | user | F6 |
| `created_at`, `last_message_at`, `last_inbound_at` | date | F6 |
| `marketing_opt_out` | boolean | F6 |
| `field.<key>` (email, company, address, source, lifecycle_stage, org-defined) | per definition | 01 |
| `conversation.status`, `conversation.assignee_id`, `conversation.team_id` | option / user | 03 |
| `task.has_open`, `task.has_overdue` | boolean | 04 |
| `segment` (in / not in segment) | segment | 05 |
| `deal.stage_id`, `deal.status`, `deal.pipeline_id` | option | 07 |

- **Operators by type:**
  - text: `equals`, `not_equals`, `contains`, `starts_with`, `is_empty`, `is_not_empty`
  - number: `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `between`, `is_empty`
  - date: `on`, `before`, `after`, `between`, `within_last`, `more_than_ago`, `is_empty` (evaluated in org timezone, F9)
  - option: `in`, `not_in`, `is_empty`
  - tags: `contains_any`, `contains_all`, `contains_none`, `is_empty`
  - boolean: `is_true`, `is_false`
  - user: `is`, `is_not`, `is_me`, `is_unassigned`
- **Compiler:** `contactquery.Apply(db *gorm.DB, orgID uuid.UUID, viewer Viewer, f Filter) (*gorm.DB, error)`. All values are bound parameters; field and column names come only from the registry. Custom-field and relational rules compile to `EXISTS (…)` subqueries.
- **Access scope:** `contactquery.Scope(viewer)` moves `scopeAssignedContact` (`contacts.go:208`) into the package, so tasks, timeline, deals and segments share one "who can see this contact" rule.

### Contacts list v2 API
- `POST /api/contacts/search`, with body `{ "filter": <AST>, "search": "…", "sort": [{"field":"last_message_at","dir":"desc"}], "page": 1, "limit": 50, "include": ["fields","unread"] }`.
- The existing `GET /api/contacts` stays for the chat list until 03 moves chat to `/api/inbox`; internally it calls the same compiler.
- Unread counts come from a single grouped query over the page's contact IDs.
- `GET /api/contacts/filter-fields` returns the registry (key, label, type, operators, options) for the UI builder.
- Sort whitelist: `last_message_at`, `created_at`, `profile_name`, `field.<key>` (text/number/date only).

### Frontend
`components/contacts/FilterBuilder.vue` renders the registry dynamically: add rule, group AND/OR, per-type value inputs (option multiselect, date presets, user picker). The Contacts module (01), segments (05), automation conditions (08) and campaign audience (05) all reuse it.

---

## F7 — Phone normalisation (`internal/phoneutil`)

### Current state
The only rule is "strip a leading `+`", repeated in `contactutil.go:20,77`, `contacts.go:1351` and `import_export.go:131`. Send-template-by-phone (`messages.go:789`) does not even do that. There is no E.164 handling, so `03219173161` and `923219173161` are different contacts.

### Design
- `phoneutil.Normalize(raw string, defaultCountry string) (normalized string, ok bool)`:
  - trim, remove spaces, `-`, `(`, `)`, `.`
  - leading `+` → drop; leading `00` → drop
  - leading trunk `0` + `defaultCountry` set → replace with the country calling code
  - validate with `github.com/nyaruka/phonenumbers` (Go port of libphonenumber), returning digits without `+`
  - group JIDs (contain `-` or `@`) are returned unchanged and `ok=false`
- `organizations.settings.default_country_code` (ISO-3166 alpha-2, optional), editable in Settings → General.
- New column `contacts.phone_normalized varchar(32)` with non-unique index `(organization_id, phone_normalized)`. It is **not** used for sending (`phone_number` stays the WhatsApp ID).
- Set on every write path: `contactutil.GetOrCreateContact`, `CreateContact`, `UpdateContact`, import, send-by-phone.
- Lookup order in `GetOrCreateContact`: exact `phone_number` → `phone_normalized` → `contact_identities` (06). Plan 06 extends this.
- F1 migration backfills `phone_normalized` for existing rows.

---

## F8 — `contact_id` on campaign recipients

- Add `bulk_message_recipients.contact_id uuid null` with index `(campaign_id, contact_id)` and `(contact_id)`.
- The worker (`worker/worker.go:94`) already calls `GetOrCreateContact`; it stores the ID on the recipient row.
- Segment-materialised recipients (05) set it at insert.
- F1 backfill: join recipients → campaigns → contacts on org + phone (exact, then normalized).
- The worker also publishes `campaign.sent_to_contact` (activity not recorded; the timeline reads recipients directly).

---

## F9 — Organisation and user timezone

- `organizations.settings.timezone` (IANA, default `UTC`), editable in Settings → General with a searchable select.
- `users.settings.timezone` (optional override), editable in Profile.
- `timeutil.OrgLocation(orgID)` / `timeutil.UserLocation(user)`, cached in Redis with the org settings.
- Used by:
  - "due today" boundaries (04)
  - snooze presets such as "tomorrow 9am" (03)
  - date filter operators (F6)
  - report bucketing (09)
  - chatbot business hours, which currently have no explicit timezone (document the assumption)
- Frontend `lib/utils.ts` date helpers accept an optional timezone; the auth store exposes the effective zone.

---

## F10 — Messaging service extraction (`internal/messaging`)

### Current state
- `App.SendOutgoingMessage` (`messages.go:147`) is reusable from goroutines (the SLA processor uses it), but it needs `*App`.
- Validation lives only in the HTTP handler `SendTemplateMessage` (`messages.go:662-927`): template approved, parameter count, marketing opt-out, OTP auto-fill.
- The worker duplicates the send path (`worker.go:256`) and omits `ButtonURLParams`.
- The 24-hour window is not enforced server-side.

### Design
- `messaging.Service` built from DB, WhatsApp client, bus (F2), log. `App` and `Worker` both hold one.
  - `PrepareTemplate(ctx, PrepareInput{OrgID, Account, Contact, TemplateID|Name, BodyParams, HeaderParams, ButtonURLParams}) (Prepared, error)` — all validation from the handler, returning typed errors (`ErrTemplateNotApproved`, `ErrMissingParams`, `ErrMarketingOptOut`).
  - `SendTemplate(ctx, Prepared, SendOptions)` and `SendText(ctx, …, SendOptions{RequireServiceWindow bool})`.
- The HTTP handler, campaign worker, SLA processor, automation (08) and segment campaigns (05) all call the service.
- Automated sends (08) set `RequireServiceWindow=true` for free text, so they fail fast with `ErrOutsideServiceWindow` instead of being rejected by Meta.
- **Refactor guard:** keep `SendOutgoingMessage` as a thin wrapper so existing call sites and tests keep compiling; existing message send tests must pass unchanged.

---

## F11 — Webhook hardening

1. **Fix unsigned deliveries (fix-first):** `Webhook.Secret` is `json:"-"` (`models.go:262`) while `getWebhooksCached` serialises webhooks to Redis as JSON (`cache.go:268-290`), so cache hits lose the secret. Cache a dedicated struct that includes the (already stored) secret, or cache IDs and load secrets from DB. Add a regression test that dispatches twice and asserts both requests carry `X-Webhook-Signature`.
2. **New events:** extend `AvailableWebhookEvents` (`webhooks.go:110`) with the catalog entries marked `webhook: yes`. Group them in the UI by area (Contacts, Conversations, Tasks, Deals).
3. **Delivery log (recommended before 08):** `webhook_deliveries (id, organization_id, webhook_id, event, status, attempts, response_code, error, duration_ms, created_at)`, retained 14 days. Shown on the webhook detail page. Automation `call_webhook` actions (08) write to the same log with `webhook_id = null`, `automation_rule_id` set.

---

## Testing strategy for foundations
- Unit tests: `phoneutil` (country table, JIDs), `contactquery` compiler (every operator, SQL injection attempts on field names, depth/size limits), `crmevents` catalog completeness (every catalog type has payload + flags).
- Handler tests (`TEST_DATABASE_URL`, `TEST_REDIS_URL`): permission backfill on an org with existing roles, scheduler lock with two schedulers in one test, notification CRUD scoped to the user, webhook signing on cache hit.
- Multi-replica smoke: two server processes + worker against one Redis; a tag change on one replica reaches a browser on the other.

## Risks
- **Event bus becomes a hidden coupling point.** Mitigation: the catalog is explicit, sinks are ordered and each is individually testable, and there are no business rules inside sinks.
- **Redis stream growth.** Mitigation: `MAXLEN ~`, skip orgs without automations.
- **Phone library size.** `nyaruka/phonenumbers` embeds metadata (~several MB). Acceptable for a server binary; if not, fall back to a trimmed country-code table.


---

## Integration addendum (from [10 — System integration](10-system-integration.md))

These changes supersede conflicting text above.

- **F1:**
  - Permission backfill also covers **custom roles** that hold the closest existing permission (for example `contacts:write` → `tasks:write`).
  - A grant a super admin explicitly removed is never re-added (`role_permission_revocations`).
  - Seeds for new orgs go through `orgseed.Seed` (S8), not separate migrations.
  - Tags `import`/`export` and `tasks:export`/`deals:export` permissions are seeded (X11).
- **F2:**
  - The in-process `PublishTx` closure is replaced by the **transactional outbox** (`crm_event_outbox`, S3). Events and activity rows are written in the business transaction; a relay fans out to the stream, the WS channel and webhooks.
  - The worker, calling manager and scheduler publish by inserting outbox rows.
  - Envelopes carry `event_id`; webhook payloads add `delivery_id`.
- **F3:**
  - The instrumentation list adds: transfer-driven owner writes (to be removed per S5), import update, contact restore/delete (`contact.restored` / `contact.deleted`), `PickNextTransfer`, `ReturnAgentTransfersToQueue`.
  - Actor types add `bot`.
- **F4:**
  - Add the outbox relay, chatbot session timeout marking, conversation idle auto-resolve, and template-dependency checks to the job table.
  - All time evaluation uses `internal/schedule` (S11).
- **F5:**
  - The bell sits next to the user menu at the sidebar bottom and in the mobile top bar, not in the logo row (hidden on mobile and when collapsed).
  - Per-user preferences move to **Profile** for all users. Today they are in Settings → Notifications behind `settings.general`, which agents cannot open. This needs `UpdateCurrentUserSettings` extended (`users.go:715-750`).
  - Existing hardcoded English toasts in `websocket.ts` move to i18n.
- **F6:**
  - `Scope` = contact visibility **plus conversation visibility**: unassigned conversations in the viewer's teams must be openable, not only listed (S9).
  - Every serializer applies phone masking through the cached helper.
  - The FilterBuilder lives in `components/shared` and is registry-driven for contacts, tasks and deals, with `useListViewState` for URL and saved views (S12).
- **F7:** the lookup chain is implemented inside the Contact Lifecycle service (S2).
- **F8:**
  - The worker resolves contacts with `AllowRestore:false, UpdateName:false`.
  - The recipient unique index excludes soft-deleted rows.
  - Duplicate conflicts fail the recipient permanently so the campaign still completes.
- **F9:**
  - Org `timezone` and `date_format` **already exist** (`organization.go:29-94`). Scope becomes: full IANA select, per-user override, and applying both through `useFormatters` and `internal/schedule`.
  - API date ranges are interpreted in the org timezone.
- **F10:**
  - The scope includes the **campaign worker and echo ingestion** (X3).
  - Adds `messages.sender_type` and the counters trigger (S4).
  - The worker inserts rows before calling Meta.
- **F11:**
  - Event names are validated against the catalog (`CreateWebhook` accepts anything today).
  - Catalog entries carry `group` / `label_key`.
  - Docs are updated (`features/webhooks.mdx`).
