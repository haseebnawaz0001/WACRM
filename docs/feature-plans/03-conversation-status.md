# 03 — Conversation Status

| | |
|---|---|
| **Priority** | **P0** |
| **Phase** | 1 |
| **Effort** | 3 engineer-weeks |
| **Depends on** | F1 migrations, F2 events, F3 activity, F4 scheduler, F5 notifications, F9 timezone |
| **Unlocks** | 04 tasks (link to conversation), 06 merge (conversation re-pointing), 08 automation (conversation triggers, no-reply), 09 response/resolution times |
| **Status** | Proposed |

## 1. Problem
There is **no conversation entity**. A "conversation" today is spread across three places:
- the contact row: `last_message_at`, `is_read`, `assigned_user_id`, `chatbot_last_message_at`
- `agent_transfers` (active/resumed/expired, with SLA)
- `chatbot_sessions`

`messages.conversation_id` exists (`models.go:386`) but is never written. Only transfers have a status, so:
- agents cannot mark work done, waiting-on-customer or snoozed
- the inbox cannot show "my open", "unassigned" or "snoozed"
- the SLA processor mixes transfer-level SLAs with conversation-level concerns (client inactivity, auto-close)
- first-response and resolution times cannot be measured per conversation (`AvgFirstResponseMins` is never populated, `agent_analytics.go`)

## 2. Goals
1. **Conversation record:** a first-class `conversations` record per contact thread, with status **Open, Pending, Snoozed (until a date), Resolved**.
2. **Inbox views and filters:** **Mine**, **Unassigned**, **Bot**, **All**, each filterable by status, team, account and tags, with counts.
3. **Automatic transitions** on customer messages, agent replies, transfers, SLA auto-close and snooze expiry.
4. **Assignment:** one clear notion of "who is handling this now" (conversation assignee), distinct from the contact owner (`contacts.assigned_user_id`).
5. **Measurable timings** that 09 uses: first response, waiting time, resolution time.

### Non-goals (v1)
- Multiple parallel conversations per contact (e.g. one per WhatsApp account). The contact is shared across accounts today (unique per org, not per account); one open conversation per contact matches that.
- Priority, CSAT surveys, macros.
- Merging conversations (contacts merge in 06).

## 3. Current state
| Area | Fact | Ref |
|---|---|---|
| Transfers | One active transfer per contact; sources manual/flow/keyword/chatbot_disabled; `PickNextTransfer` FIFO with `SKIP LOCKED`; resume = end handoff | `agent_transfers.go:398,626,839,1067` |
| Assignment | Team strategies in `internal/assignment/assigner.go`; `AssignToSameAgent` uses `contact.assigned_user_id` | `assigner.go:36-154` |
| Contact owner | `contacts.assigned_user_id`, set by transfers only when empty | `agent_transfers.go:523,786` |
| SLA processor | auto-close expired transfers, escalation, breach, client inactivity reminders/auto-close | `sla_processor.go:100-560` |
| Chat list | `GET /api/contacts` ordered by `last_message_at`; unread N+1; `new_message` WS → full refetch | `contacts.go:137,155`, `websocket.ts:375` |
| Visibility | Users without `contacts:read` see assigned contacts or contacts with an active transfer to them; default agent role has `contacts:read` | `contacts.go:208`, `roles.go:292` |
| Bot pause | Frontend-only "Paused" badge; backend skips chatbot while a transfer is active | `ChatView.vue:1908`, `chatbot_processor.go:200` |

## 4. Design

### 4.1 Data model
```sql
conversations (
  id                        uuid pk default gen_random_uuid(),
  organization_id           uuid not null,
  contact_id                uuid not null,
  status                    varchar(20) not null default 'open', -- open | pending | snoozed | resolved
  assignee_id               uuid null,       -- user handling now
  team_id                   uuid null,
  bot_active                boolean not null default true,  -- chatbot handling, no human transfer
  whatsapp_account          varchar(100) not null default '', -- last account used
  snoozed_until             timestamptz null,
  snoozed_by_id             uuid null,
  opened_at                 timestamptz not null,
  first_customer_message_at timestamptz null,
  first_response_at         timestamptz null, -- first human agent outbound after opened_at
  first_responder_id        uuid null,
  last_customer_message_at  timestamptz null,
  last_agent_message_at     timestamptz null,
  last_message_at           timestamptz null,
  waiting_since             timestamptz null, -- oldest unanswered customer message (null = not waiting on us)
  resolved_at               timestamptz null,
  resolved_by_id            uuid null,        -- null when resolved by system
  resolution_reason         varchar(30) null, -- agent | sla_auto_close | client_inactivity | pending_timeout | merged | bulk | automation
  reopened_count            integer not null default 0,
  message_count             integer not null default 0,
  created_at, updated_at, deleted_at
);
create unique index idx_conversations_one_active on conversations (organization_id, contact_id)
  where status <> 'resolved' and deleted_at is null;
create index idx_conversations_inbox   on conversations (organization_id, status, assignee_id, last_message_at desc);
create index idx_conversations_team    on conversations (organization_id, status, team_id, last_message_at desc);
create index idx_conversations_snoozed on conversations (snoozed_until) where status = 'snoozed';
create index idx_conversations_contact on conversations (contact_id, opened_at desc);
```
- `messages.conversation_id` (existing varchar, indexed) gets `conversations.id` on every new message.
- **Org settings** (`organizations.settings.inbox`), editable in Settings → General → Inbox:
  - `reopen_window_hours` (default 24): a customer message within this window after resolve reopens the same conversation; after it, a new conversation starts.
  - `auto_pending_on_agent_reply` (default `false`): an agent reply moves Open → Pending.
  - `auto_resolve_pending_after_hours` (default `0` = off).

### 4.2 Status meanings
| Status | Meaning | Shown in |
|---|---|---|
| **Open** | Needs attention from us (bot or human) | Mine / Unassigned / Bot / All |
| **Pending** | We replied and are waiting on the customer | Status filter "Pending" |
| **Snoozed** | Hidden until `snoozed_until` or until the customer writes | "Snoozed" |
| **Resolved** | Done | "Resolved" |

### 4.3 State machine (single service: `conversation.Service`)
All transitions go through `internal/handlers/conversations_service.go` (or `internal/conversation`). The service takes a row lock (`SELECT … FOR UPDATE`), writes the conversation, sets `messages.conversation_id` where relevant, and publishes events via `PublishTx`.

| Trigger | From | To | Side effects |
|---|---|---|---|
| Customer message (`saveIncomingMessage`, `chatbot_processor.go:1603`) | none | **open** (create) | `first_customer_message_at`, `waiting_since`, `bot_active = !hasActiveTransfer` |
| | resolved ≤ reopen window | **open** (reopen) | `reopened_count++`, clear resolved fields |
| | resolved > window | **open** (new row) | |
| | pending | **open** | `waiting_since` |
| | snoozed | **open** | notify assignee (F5 `conversation_snooze_ended`) |
| | open | open | set `waiting_since` if null |
| Human agent outbound (`SendOutgoingMessage` with `SentByUserID`) | open | open or **pending** (setting) | `first_response_at` / `first_responder_id` if null; clear `waiting_since` |
| | none/resolved (agent-initiated outreach) | **pending** (create/reopen) | |
| Bot outbound | any active | unchanged | `bot_active=true` unless a transfer is active |
| Campaign send | — | no change | does **not** create conversations; the customer's reply does |
| Transfer created (any source) | none/resolved/pending/snoozed | **open** | `bot_active=false`, `team_id`, `assignee_id = transfer.agent_id` |
| Transfer assigned / picked up | open | open | `assignee_id` → emit `conversation.assigned` |
| Transfer resumed ("hand back to bot") | open | open | `assignee_id=null`, `bot_active=true` |
| SLA auto-close (`sla_processor.go:100`) | any active | **resolved** | `resolution_reason=sla_auto_close` |
| Client inactivity auto-close (`sla_processor.go:466`) | any active | **resolved** | `resolution_reason=client_inactivity` |
| User: Resolve | open/pending/snoozed | **resolved** | if an active transfer exists → resume it (existing `ResumeFromTransfer` logic) |
| User: Mark pending | open/snoozed | **pending** | |
| User: Snooze until T (T > now) | open/pending | **snoozed** | snooze pauses SLA escalation on the active transfer (deadlines shift by the snoozed duration on wake) |
| User: Reopen | resolved/pending/snoozed | **open** | |
| Scheduler `conversation_snooze_wakeup` (F4, 1 min) | snoozed, `snoozed_until <= now` | **open** | notify assignee (or team members when unassigned) |
| Scheduler `auto_resolve_pending` (same job) | pending past threshold | **resolved** | `resolution_reason=pending_timeout` |
| Contact merged (06) | secondary's active | **resolved** | `resolution_reason=merged` |

**Concurrency:** creation uses `INSERT … ON CONFLICT DO NOTHING` against the partial unique index, then re-selects. Two webhooks for the same contact cannot create two open conversations.

### 4.4 Assignment model (resolving today's ambiguity)
| Concept | Stored in | Meaning | UI label |
|---|---|---|---|
| Conversation assignee | `conversations.assignee_id` | Who handles this conversation now | "Assigned to" |
| Contact owner | `contacts.assigned_user_id` (existing) | Relationship manager for the contact | "Contact owner" (in 01/02 UI) |
| Transfer agent | `agent_transfers.agent_id` | Handoff/queue mechanics + SLA | internal |

- **Rule:** while a transfer is active, the transfer is the source of truth and the conversation mirrors it.
- **Inbox "Assign to…":**
  - with no active transfer, creates a manual transfer (`CreateAgentTransfer`), which pauses the bot exactly as today and applies SLA
  - with an active transfer, calls the existing assign logic
- **Unassign** returns the transfer to the queue (existing behaviour).
- `AssignToSameAgent` keeps using the contact owner.

### 4.5 Backend API
| Method & path | Purpose | Permission |
|---|---|---|
| `GET /api/inbox?view=mine\|unassigned\|bot\|all\|team:<id>&status=&account=&tags=&search=&cursor=&limit=` | Conversation list joined with contact summary + unread count (single grouped query), ordered by `last_message_at desc`, keyset cursor | `chat:read` (visibility rules below) |
| `GET /api/inbox/counts?status=open` | Counts per view for badges | `chat:read` |
| `GET /api/conversations/{id}` | Detail incl. SLA snapshot of active transfer | `chat:read` |
| `GET /api/contacts/{id}/conversations` | History list (used by 02) | `contacts:read` or scoped |
| `POST /api/conversations/{id}/status` `{status, snoozed_until?}` | Transition | `chat:write` |
| `POST /api/conversations/{id}/assign` `{user_id?, team_id?}` | Assign/unassign (via transfer rules) | `chat.assign:write`; assigning to self from unassigned requires `transfers:pickup` |
| `POST /api/conversations/bulk` `{ids[], action: resolve\|pending\|snooze\|assign, payload}` | Up to 200 | `chat:write` (+ assign perm) |

**Visibility (server-side, same as transfers today):**
- Users with `contacts:read` see all conversations.
- Other users see conversations assigned to them, plus unassigned conversations whose `team_id` is one of their teams or null.
- `view=bot` requires `contacts:read`.

**Events** (F2 catalog): `conversation.created`, `conversation.status_changed` (`data: {from, to, reason, snoozed_until}`), `conversation.assigned` (`data: {from_user, to_user, team}`). All are recorded as activity and exposed as webhooks and automation triggers.

**Realtime:** `conversation_updated` (`{id, contact_id, status, assignee_id, team_id, bot_active, last_message_at, preview, unread_count}`) broadcast to the org via the F2 realtime sink.

**`new_message` WS** handling in the frontend switches from `fetchContacts()` to patching the conversation item (fixes the full refetch on every message).

**SLA processor changes** (moved onto F4 scheduler):
- De-duplicate per org: iterate distinct `organization_id` from `chatbot_settings where sla_enabled`.
- Client inactivity reads `conversations` (open, bot-handled) instead of `contacts.chatbot_last_message_at`. The contact columns remain until a follow-up cleanup.
- Auto-close and inactivity close call `conversation.Service.Resolve`.
- Snoozed conversations skip escalation.

**Agent analytics quick win** (`agent_analytics.go`):
- `AvgFirstResponseMins` = avg(`first_response_at - first_customer_message_at`) grouped by `first_responder_id`.
- Resolution = avg(`resolved_at - opened_at`) for `resolution_reason='agent'` grouped by `resolved_by_id`.
- Replace the inaccurate queue-time calculation with `agent_transfers.picked_up_at - transferred_at`.

### 4.6 Frontend
**Inbox list** (`views/chat/ChatView.vue` + new `stores/inbox.ts`, replacing the chat usage of `stores/contacts.ts` list state)
- **Views:** tabs above the list: **Mine**, **Unassigned**, **All** (+ **Bot** for users with `contacts:read`). Each shows a count badge from `/inbox/counts`.
- **Status select:** Open (default), Pending, Snoozed, Resolved.
- **Filters menu:** team, WhatsApp account (finally filters the *list*), tags.
- **Row:** avatar, name, preview, time, unread badge, plus status indicators:
  - snoozed clock with wake time
  - assignee avatar (in All / Unassigned views)
  - bot icon when `bot_active`
- **Keyboard:** `j`/`k` move, `e` resolve, `s` snooze menu, `p` pending. They are optional, documented in a `?` shortcut sheet.

**Conversation header** (existing header area in `ChatView.vue`)
- **Status control:**
  - primary button **Resolve** (or **Reopen** when resolved)
  - adjacent menu: Mark as pending, Snooze → 1 hour / Tomorrow 9:00 / Next Monday 9:00 / Custom date-time (F9 timezone)
- **Assignee chip:** "Assigned to Priya" with a picker (users filtered by availability and team), and "Unassigned" shown in amber.
- The "Paused" badge is replaced by the explicit bot/human indicator.
- **Banners:** snoozed "Snoozed until Tue 9:00 — Unsnooze"; resolved "Resolved by Alex · Reopen". Sending a message from a resolved conversation reopens it (as pending).

**Contact panel / profile:** conversation history list (status, opened/resolved, assignee, duration) in the contact profile (02).

**Settings → General → Inbox:** the three org settings in 4.1.

**Notifications (F5):** `conversation_assigned` (to the new assignee) and `conversation_snooze_ended`.

## 5. Migration & backfill
1. **Schema:** AutoMigrate `Conversation`; add indexes; add to testutil migrations.
2. **F1 migration `…_conversations_backfill`**, run in batches of 1,000 contacts, for contacts with `last_message_at` in the last **90 days**:
   - active transfer → `open`, `assignee_id`/`team_id` from the transfer, `bot_active=false`
   - last message incoming and no later outbound → `open`, `waiting_since = last inbound`
   - otherwise → `resolved`, `resolved_at = last_message_at`, `resolution_reason='agent'` if the last outbound was by a user, else `'sla_auto_close'`
   - timestamps (`first_*`, `last_*`, `message_count`) computed from messages in the window
   - `messages.conversation_id` updated for those contacts' messages in the window
   - contacts older than 90 days get a conversation on their next message
3. **Deploy order:**
   1. backend with dual-write (conversation service invoked from existing paths) and backfill
   2. `/api/inbox` endpoints
   3. frontend inbox switch
   4. SLA processor move
4. **Rollback:** the frontend can fall back to `GET /api/contacts` (unchanged) behind `VITE_INBOX_V2` during rollout.

## 6. Milestones
| # | Deliverable | Est. |
|---|---|---|
| M1 | Model, indexes, `conversation.Service` state machine + unit tests (every transition) | 3 d |
| M2 | Hooks in inbound save, outbound send, transfer create/assign/resume, SLA close paths; `messages.conversation_id` | 3 d |
| M3 | Backfill migration + verification script (counts by status) | 1 d |
| M4 | `/api/inbox`, counts, status/assign/bulk endpoints, visibility, events, WS | 3 d |
| M5 | Scheduler jobs (snooze wake-up, pending auto-resolve), SLA processor move + de-dupe | 2 d |
| M6 | Inbox UI (views, filters, header status control, snooze picker, banners, WS patching) | 3 d |
| M7 | Agent analytics quick win + settings UI + notifications | 1 d |

## 7. Testing
- **State machine table tests:** each row of 4.3, including the reopen window boundary, snooze interrupted by an inbound message, and resolve with an active transfer (transfer becomes resumed).
- **Concurrency:** two goroutines delivering inbound messages for a new contact create exactly one open conversation.
- **Visibility:** an agent without `contacts:read` sees only mine + unassigned in their teams; `view=bot` is 403.
- **Scheduler:** the snooze wake-up job with two scheduler instances (F4 lock) wakes once and sends one notification.
- **SLA:** auto-close resolves the conversation with the correct reason; escalation is skipped while snoozed.
- **E2E** (`e2e/tests/chat/inbox-status.spec.ts`):
  - an agent resolves a conversation, the customer message reopens it (simulate via webhook helper)
  - snooze → wake (time-travel via API helper that sets `snoozed_until` in the past + trigger job endpoint in test mode)
  - counts update across two browser contexts

## 8. Risks & open questions
| Risk / question | Mitigation / proposal |
|---|---|
| Three "assignment" concepts confuse users | Distinct labels ("Assigned to" vs "Contact owner"); transfer hidden as internal; docs page explaining the model |
| Hooks missed in some send path (e.g. calls, echoes from WhatsApp Business app) | Central hook in `saveIncomingMessage` and `SendOutgoingMessage`; echo path (`webhook.go:647`) counts as agent outbound with no user → no `first_response_at` |
| Backfill load on large orgs | Batched, resumable (records last contact id in `schema_migrations`-adjacent progress key) |
| Should Pending be automatic? | Off by default (explicit is less surprising); org setting |
| Per-account conversations later? | The unique index can be extended to `(organization_id, contact_id, whatsapp_account)` in a future plan; `whatsapp_account` is already stored |

## 9. Acceptance criteria
- [ ] Every new inbound message belongs to exactly one conversation, and `messages.conversation_id` is set.
- [ ] Agents can Resolve, Mark pending, Snooze (presets + custom) and Reopen; the list and header update in real time in other tabs.
- [ ] Inbox views Mine / Unassigned / Bot / All with status filter and accurate counts; the WhatsApp account filter narrows the list.
- [ ] A customer reply reopens resolved (within window), pending and snoozed conversations.
- [ ] Snoozed conversations wake at the chosen time (org/user timezone) exactly once and notify the assignee.
- [ ] SLA auto-close and client inactivity resolve conversations with a reason; the SLA job runs once per org per tick across replicas.
- [ ] Agent Analytics shows first response and resolution times per agent from conversation data.
- [ ] Webhooks `conversation.created/status_changed/assigned` are delivered (signed) when subscribed.


---

## Integration addendum (from [10 — System integration](10-system-integration.md))

**Mechanics** (S4, S5):
- **Handling state:** replace `bot_active` with `handling` (`bot | human | handoff_pending | none`), derived from real state. Out-of-hours suppressed handoffs are `handoff_pending` and appear in Unassigned.
- **Locking:** per-contact advisory lock and canonical lock order (contact → conversation → transfer); unique partial index on active `agent_transfers`.
- **Transfer service:** `internal/transfers.Service` with typed outcomes. **`ReturnAgentTransfersToQueue`** (`agent_transfers.go:1377`, called when an agent goes away), `PickNextTransfer` and SLA expiry are hooked too.
- **Contact owner:** transfers **never clear** `contacts.assigned_user_id` (today they do on unassign and return-to-queue).
- **Resolve:** resolve and auto-close **end the chatbot session**. Idle auto-resolve (`inbox.auto_resolve_idle_hours`) works without SLA enabled.
- **Response counters:**
  - `first_response_at` and `last_agent_message_at` count only `sender_type=agent`, and only after a successful send.
  - Counters are maintained by the DB trigger.
  - `waiting_since` and reopen decisions use message timestamps, not `now()`.
- **Non-message inbound:** call-permission replies and reactions call `TouchInbound`.

**Visibility and reading:**
- Visibility is enforced for **opening** a conversation, not only listing it (S9).
- **Per-user read state** (`conversation_reads`) with a supervisor **peek** mode; WhatsApp read receipts are sent only for the assignee.
- `CurrentConversationOnly` uses `conversations.opened_at`.

**UI** (10 §4.1, §4.2):
- **Store:** evolve `stores/contacts.ts` into the inbox store; do not add a parallel `stores/inbox.ts`. Update all `fetchContacts()` callers (`websocket.ts`, `UserMenu.vue`, `ChatView.vue`).
- **Search:** falls back to all contacts, so contacts without a conversation (imported, or outside the backfill window) stay reachable.
- **Assignment:** a **unified assignment control** replaces three things: the "Assign Contact" dialog (which sets the owner and is gated by role name), the "Transfer to agent" item and the Resume button. Contact owner moves to the sidebar header.
- **Transfers page:** absorbed into the inbox. Unassigned = queue with team filter and Pick next; the supervisor SLA view stays as a tab; `/chatbot/transfers` redirects.
- **Realtime:** list patching follows view membership rules; toasts go to the assignee; transfer and call toasts deep-link to `/chat/:id` (S10).
- **List rows:** show `last_message_preview` (add it to the frontend `Contact` type) and an SLA chip.
- **Settings:** inbox settings live on **Settings → Inbox**, together with SLA and business hours moved from `/settings/chatbot` (10 §4.8).
