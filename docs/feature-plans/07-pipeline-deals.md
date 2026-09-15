# 07 — Pipeline / Deals (Kanban Board)

| | |
|---|---|
| **Priority** | **P3** by default · **P1 for orgs that sell or book** (move to Phase 2 for those) |
| **Phase** | 4 |
| **Effort** | 3 engineer-weeks |
| **Depends on** | 01 (custom fields with `entity_type='deal'`), 02 (profile panel + timeline renderer), 04 (tasks on deals), F2 events, F3 activity, F6 query registry, F9 timezone (close dates), F11 webhooks |
| **Unlocks** | 08 deal triggers/actions (catalog entries already reserved), 09 pipeline funnels, win rate, forecast |
| **Status** | Proposed |

## 1. Problem
There is no way to track opportunities, bookings or cases that move through stages. A team selling over WhatsApp tracks "interested → quoted → negotiating → won" in spreadsheets or tags, with no value, owner, expected close date or history.

## 2. Goals
1. **Pipelines per org:** each pipeline has ordered **stages** (probability, won/lost markers, colour, rotting threshold).
2. **Deals:** each deal is linked to a contact, with a title, value + currency, owner, expected close date, status (open/won/lost), lost reason, and custom fields (reusing 01 with `entity_type='deal'`).
3. **Kanban board:** drag-and-drop between stages, stage totals (count and sum of value), filters (owner, close date, value), quick edit. Also a list/table view.
4. **History:** stage moves are logged to `deal_stage_history` (for conversion reports) **and** to the contact timeline.
5. **Configurable object label** per pipeline ("Deal", "Case", "Appointment", "Booking") so service or support businesses use the same structure.

### Non-goals (v1)
- Products/line items, quotes/invoices, multi-currency conversion, weighted forecasting UI beyond per-stage probability totals.
- Appointment calendar scheduling (a date custom field on the "Appointment" pipeline covers basic use; real calendar booking is a separate plan).
- Multiple contacts per deal (single primary contact in v1).

## 3. Current state
- No deal/pipeline concepts exist.
- `vuedraggable` is installed but unused (frontend research), so it is ready for Kanban.
- 01 designs `custom_field_definitions.entity_type` so deals reuse the field system.
- 08's trigger/action catalog reserves `deal.*` entries; 09 reserves pipeline reports.

## 4. Design

### 4.1 Data model
```sql
pipelines (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  name varchar(100) not null,
  object_label_singular varchar(40) not null default 'Deal',   -- "Case", "Appointment", "Booking"
  object_label_plural   varchar(40) not null default 'Deals',
  currency char(3) not null default 'USD',  -- default for new deals; org default from organizations.settings.currency
  is_default boolean not null default false,
  position integer not null default 0,
  archived_at timestamptz null,
  created_by_id uuid null,
  created_at, updated_at, deleted_at
);
create unique index idx_pipelines_org_name on pipelines (organization_id, lower(name)) where deleted_at is null;

pipeline_stages (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  pipeline_id uuid not null,
  name varchar(100) not null,
  position integer not null,
  stage_type varchar(10) not null default 'open', -- open | won | lost
  probability smallint not null default 0,        -- 0-100, for weighted totals
  color varchar(20) not null default 'gray',
  rotting_days integer not null default 0,        -- 0 = off; card flagged if no stage change for N days
  archived_at timestamptz null,
  created_at, updated_at, deleted_at
);
create index idx_pipeline_stages_pipeline on pipeline_stages (pipeline_id, position);

deals (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  pipeline_id uuid not null,
  stage_id uuid not null,
  contact_id uuid not null,
  conversation_id uuid null,               -- where it originated (03)
  title varchar(255) not null,
  value numeric(14,2) not null default 0,
  currency char(3) not null,
  owner_id uuid null,
  expected_close_date date null,
  status varchar(10) not null default 'open',  -- open | won | lost (mirrors stage_type on move)
  lost_reason varchar(255) not null default '',
  won_at timestamptz null, lost_at timestamptz null,
  stage_entered_at timestamptz not null,       -- for rotting + time-in-stage
  board_position varchar(32) not null,         -- lexorank-style string for ordering within a stage
  source varchar(20) not null default 'manual', -- manual | automation | api | chat
  created_by_id uuid null,
  created_at, updated_at, deleted_at
);
create index idx_deals_board   on deals (organization_id, pipeline_id, stage_id, board_position) where deleted_at is null;
create index idx_deals_contact on deals (contact_id, status);
create index idx_deals_owner   on deals (organization_id, owner_id, status, expected_close_date);

deal_stage_history (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  deal_id uuid not null,
  pipeline_id uuid not null,
  from_stage_id uuid null,                 -- null on creation
  to_stage_id uuid not null,
  moved_by_id uuid null,                   -- null for automation/system
  actor_type varchar(20) not null,
  time_in_from_stage_seconds bigint null,
  value_at_move numeric(14,2) not null,
  moved_at timestamptz not null
);
create index idx_deal_stage_history_deal     on deal_stage_history (deal_id, moved_at);
create index idx_deal_stage_history_pipeline on deal_stage_history (organization_id, pipeline_id, moved_at);
```
- **Deal custom fields:** `custom_field_definitions` rows with `entity_type='deal'` (optionally scoped per pipeline via `validation.pipeline_ids`); values in `custom_field_values` with `entity_type='deal'`. There are no system deal fields.
- **Board ordering:** a `board_position` string (lexorank). A move sets a key between its neighbours, so no mass renumbering is needed. A nightly F4 job rebalances stages whose keys exceed 30 chars.
- **Default pipeline** (seeded per org by an F1 migration; editable, deletable if unused):
  - "Sales": New lead (10%), Qualified (25%), Proposal sent (50%), Negotiation (75%), **Won** (won, 100%), **Lost** (lost, 0%)

### 4.2 Backend
**Package** `internal/deals` (service) + handlers `pipelines.go`, `deals.go`.

**Service rules**
- **Creating a deal:**
  - validates the pipeline/stage belong together and to the org, and that the contact is accessible (F6 scope)
  - `currency` defaults to the pipeline's
  - `stage_entered_at = now`
  - inserts `deal_stage_history(from=null)`
  - emits `deal.created`
- **Moving stage** (`MoveDeal(dealID, toStageID, beforeID?, afterID?)`):
  - row lock; compute `board_position`
  - if the stage changes: history row with `time_in_from_stage_seconds`; `stage_entered_at = now`; status mirrors `stage_type` (won → `won_at`, lost → requires `lost_reason` unless the org setting allows empty)
  - emits `deal.stage_changed` (+ `deal.won` / `deal.lost`)
  - reordering within the same stage emits nothing but updates position
- **Reopening** a won/lost deal requires moving it to an open stage; this clears `won_at` / `lost_at`.
- **Deleting a stage:** only if no deals, or with `move_deals_to` specified. Deleting a pipeline: only if no open deals (or archive).
- **Activity (F3):** all deal events recorded on the contact with `data: {deal_id, title, pipeline, from, to, value}`; renderer `TimelineDeal.vue` (02).

**Routes**
| Method & path | Purpose | Permission |
|---|---|---|
| `GET/POST /api/pipelines`, `GET/PUT/DELETE /api/pipelines/{id}` | Pipelines CRUD (+ `archive`) | `pipelines:read/write/delete` |
| `POST /api/pipelines/{id}/stages`, `PUT /api/pipeline-stages/{id}`, `PUT /api/pipelines/{id}/stages/reorder`, `DELETE /api/pipeline-stages/{id}?move_deals_to=` | Stages | `pipelines:write/delete` |
| `GET /api/pipelines/{id}/board?owner_id=&close_from=&close_to=&search=&status=open` | Board: stages with `{count, total_value, weighted_value, deals[first 50 by position]}` | `deals:read` |
| `GET /api/pipelines/{id}/stages/{stageId}/deals?cursor=` | Load more cards in a column | `deals:read` |
| `GET /api/deals?pipeline_id=&stage_id=&owner_id=&status=&contact_id=&close_from=&close_to=&search=&sort=&page=&limit=` | List/table view | `deals:read` |
| `POST /api/deals` / `GET/PUT/DELETE /api/deals/{id}` | CRUD (fields via `fields` map, as in 01) | `deals:read/write/delete` |
| `POST /api/deals/{id}/move` `{stage_id, before_id?, after_id?, lost_reason?}` | Drag-and-drop | `deals:write` |
| `GET /api/deals/{id}/history` | Stage history | `deals:read` |
| `GET /api/contacts/{id}/deals` | Profile panel | contact access + `deals:read` |

**Visibility:**
- Users with `contacts:read` see all deals.
- Others see deals they own, or deals on contacts they can access.
- Editing others' deals requires `deals:write` + `contacts:read` (manager default); agents edit their own.

**Integrations**
- **F6 registry fields:** `deal.pipeline_id`, `deal.stage_id`, `deal.status`, `deal.has_open` (EXISTS over deals), so segments (05) can target "contacts with an open deal in Negotiation".
- **Tasks (04):** `tasks.deal_id`; creating a task from a deal card sets both `contact_id` and `deal_id`.
- **Automation (08):** triggers `deal.created`, `deal.stage_changed` (pipeline + to stages), `deal.won`, `deal.lost`; actions `create_deal`, `move_deal_stage`.
- **Webhooks (F11):** `deal.created`, `deal.stage_changed`, `deal.won`, `deal.lost`.
- **Merge (06):** re-point `deals.contact_id`.
- **Rotting:** F4 job `deal_rotting` (hourly) flags nothing in the DB; the board computes `is_rotting = rotting_days > 0 and now - stage_entered_at > rotting_days`. Optional notification to the owner once when a deal starts rotting (`deal_rotting` notification type, guarded by `data` flag).

**WS:** `deal_updated` (`{deal, from_stage_id, to_stage_id, board_position}`) broadcast to the org (clients filter by open board) for live multi-user boards.

### 4.3 Frontend
- **Navigation:**
  - CRM section → **Pipeline** (`/pipeline`, icon `KanbanSquare`, `permission: 'deals'`). The label uses the default pipeline's plural label when only one pipeline exists (e.g. "Bookings").
  - Settings → **Pipelines** (`/settings/pipelines`).
  - Update `navigationOrder`.

**Board** (`views/pipeline/PipelineBoardView.vue`)
- **Toolbar:** pipeline switcher, view toggle (Board / List), filters (owner incl. "Me", expected close range, status open/won/lost, search), "New {object}" button.
- **Columns:**
  - header with stage name, count, total value (and weighted value on hover)
  - cards (`DealCard.vue`): title, contact name + avatar, value (org locale currency formatting), owner avatar, expected close date (red when past), open task indicator (04), rotting amber edge, custom field badges configured via `show_in_list`
- **Drag and drop:**
  - `vuedraggable` across columns
  - optimistic move, then `POST /deals/{id}/move` with neighbour ids; on error, revert + toast
  - dropping on a **Lost** stage opens a small lost-reason popover
  - dropping on **Won** confirms value
- **Performance:** columns lazy-load 50 cards with "Load more" at the bottom; board filters apply server-side.
- **Mobile:** horizontal scroll snap per column; a "Move to…" menu on each card instead of drag.
- **Accessibility:** cards are focusable; `Ctrl/⌘ + ←/→` moves to the adjacent stage; an ARIA live region announces "Moved to Negotiation".

**List view:** DataTable with columns title, contact, stage, value, owner, close date, status, created, plus server sort and bulk change owner/stage.

**Deal detail** (`DealDetailSheet.vue`, a right-side sheet from board or list)
- Editable title, value, currency, owner, close date, stage and custom fields (`ContactFieldsForm` with `entity_type='deal'`).
- Stage history timeline (from `/history`), tasks linked to the deal (04), link to contact profile and originating conversation.

**Contact integrations**
- Profile (02) right column **Deals** panel (open deals with stage chip + value, "Create deal").
- Timeline renderer `TimelineDeal.vue`.
- Chat side panel compact **Deals** section with "Create deal from this conversation" (prefills contact + conversation).

**Settings → Pipelines** (`views/settings/PipelinesView.vue`)
- Pipeline list (name, object label, currency, default).
- Stage editor with drag reorder, per-stage type (open/won/lost), probability, colour, rotting days.
- Deleting a stage with deals prompts for a target stage.
- Deal custom fields: link to Contact Fields settings filtered to `entity_type=deal` (01 settings page gains an entity tab: Contacts | Deals).

## 5. Migration & rollout
1. Schema + indexes; add models to testutil.
2. F1 migrations:
   - default "Sales" pipeline per org
   - permissions: `pipelines` (admin all; manager read/write; agent read), `deals` (admin/manager all; agent read/write)
   - `organizations.settings.currency` (default `USD`; editable in Settings → General)
3. **Feature visibility:** org setting `features.pipeline` (default **off** for existing orgs, on for new orgs). Settings → General toggles it, keeping the nav clean for support-only businesses.

## 6. Milestones
| # | Deliverable | Est. |
|---|---|---|
| M1 | Models, seeds, services (create, move with lexorank + history + status mirroring), tests | 3 d |
| M2 | Pipelines/stages/deals/board/list/history APIs, visibility, events, webhooks, WS | 3 d |
| M3 | Deal custom fields (entity_type) + F6 registry fields + tasks link + merge re-point | 1.5 d |
| M4 | Board UI (columns, cards, drag/drop, lost/won popovers, filters, load more, keyboard moves) | 3.5 d |
| M5 | List view, deal sheet, profile/chat panels, timeline renderer, settings pages | 3 d |

## 7. Testing
- **Moves:**
  - lexorank ordering stability under 1,000 random moves
  - status mirroring (open→won sets `won_at`; won→open clears it)
  - lost requires a reason when configured
  - history `time_in_from_stage_seconds` correct
- **Concurrency:** two users moving the same deal → last write wins, both history rows recorded in order, WS updates both boards.
- **Stage deletion** with `move_deals_to`, and a pipeline delete blocked with open deals.
- **Visibility:** an agent sees their own deals + deals on accessible contacts only.
- **F6:** a segment "has open deal in stage X" returns the correct contacts.
- **E2E** (`e2e/tests/pipeline/*`, page object `PipelineBoardPage`):
  - create deal from chat → appears in New lead
  - drag to Proposal sent → timeline entry on contact
  - drag to Lost with reason → list view shows lost

## 8. Risks & open questions
| Risk / question | Mitigation / proposal |
|---|---|
| Support-only orgs see irrelevant UI | Feature toggle per org; object labels let them model "Cases" instead |
| Drag-and-drop accessibility | Keyboard moves + "Move to…" menu + live region |
| Currency mixing in totals | Totals only sum deals in the pipeline currency; other currencies shown as separate line ("+ €1,200 in EUR") |
| Multiple contacts per deal (B2B) | Out of scope; model allows a future `deal_contacts` join table |
| Appointment use case needs time slots | Use a date custom field in v1; dedicated booking plan later |

## 9. Acceptance criteria
- [ ] Admins can create pipelines with ordered stages (open/won/lost, probability, colour, rotting) and a custom object label; a default Sales pipeline exists.
- [ ] Users can create deals linked to a contact from the board, contact profile and chat, with value, owner, expected close date and custom fields.
- [ ] The board shows per-stage counts and value totals; dragging a card changes stage for all viewers in real time and records stage history.
- [ ] Moving to Won/Lost sets the deal status (lost reason captured); reopening works.
- [ ] Deal events appear on the contact timeline and are available as webhooks, segment filters and automation triggers.
- [ ] The pipeline module can be switched off per org.


---

## Integration addendum (from [10 — System integration](10-system-integration.md))

- **UI building blocks:**
  - Deal detail uses the shared **RecordSheet**; the list view uses DataTable v2 + `useListViewState` (S12).
  - Deals render as a **ContactSidebar** section in chat and profile.
- **Realtime:** the board subscribes to `board:<pipeline_id>`. Payloads carry ids and positions; clients refetch cards they cannot see (S10).
- **Navigation:** the pipeline feature flag is a `feature` field on the nav item in the single navigation source. Pipelines and stages are configured from the Pipeline header (gear), not a Settings sidebar child (10 §4.8, §4.9).
- **Creation entry points:** slash command `/deal`, message action "Create deal", and the chatbot/automation `create_deal` action from the shared S7 library.
- **Owners and references:**
  - Deactivated owners are handled per S8 ("Owner inactive" + reassign prompt).
  - Stage deletion and pipeline archive go through `entityrefs`, so segment filters and automation triggers that reference a stage are rewritten or flagged.
- **Chat thread:** deal stage changes also appear as chat thread pills (shared activity renderers).
