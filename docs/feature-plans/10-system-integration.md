# 10 — System Integration Review and Unified Architecture

| | |
|---|---|
| **Priority** | **P0** — the "Phase 0 platform spine" items must land before Phase 1 features |
| **Scope** | How plans 00–09 plug into the existing product, where they were wrong, and the shared architecture that makes them one system |
| **Method** | Five code reviews (chat/inbox UI, chatbot/IVR/SLA, campaigns/settings/admin, global UX, backend lifecycle & integrity) against every plan, plus live verification of the highest-risk findings |
| **Status** | Proposed — supersedes conflicting statements in plans 00–09 (each plan has an *Integration addendum* pointing here) |

---

## 1. Verdict

The feature plans are sound *as features*, but they integrate **locally**: each one hooks the one or two code paths it knows about and adds its own panel, settings page, variable syntax, action implementation and realtime event. Measured against the real code, that produces three kinds of problems:

1. **Platform gaps that break any integration.** These are not caused by the plans, but every plan depends on them:
   - no server-side permission enforcement in most handlers
   - contacts auto-restored on every send
   - WhatsApp accounts referenced by *name*
   - side effects fired from goroutines and lost on crash
   - send paths that bypass the shared sender
2. **Plan claims that do not match the code.** For example: the org timezone already exists; the chat "Assign" dialog sets the *contact owner*, not a transfer; template params are named or positional; campaigns never pass through `SendOutgoingMessage`; the chat panel never loads the fields the plan adds.
3. **Missing seams between features:**
   - four variable syntaxes
   - two action implementations (automation vs. chatbot)
   - no CRM data in chatbot/AI/IVR
   - notes and system events outside the chat thread
   - a Transfers page that would duplicate the new inbox
   - contact names that link nowhere

The fix is a **shared spine** (section 3) built first, then features that plug into it, plus a concrete **integration map per existing surface** (section 4).

---

## 2. Issues to fix before Phase 1 (fix-first)

Legend: ✅ verified live or by direct code inspection during this review; 🔎 reported by a reviewer with file references (verify during implementation).

> **Status (Phase 0 fix-first): all 14 closed.** Each row below is marked
> **[FIXED]** with where the fix lives. X1 and X2 landed in `619a6f1`; the rest
> are covered by regression tests, several of which were first confirmed to fail
> against the unfixed code. S6 (one renderer) and the X5 half of S8 (rename
> cascade) are also done. S7 now has its chatbot side: a **CRM action** node and
> a **CRM condition** node run the shared library from a flow, `save_to_field`
> writes a prompt's answer onto the contact record, and keyword rules carry an
> optional action list validated at save time. **S7 is now complete:** the
> automation builder, the chatbot CRM-action node and keyword rules all render
> the shared `components/crmactions/` editor; api_call nodes have
> `field_mapping` (writing straight to contact fields, where `response_mapping`
> only reaches session state); and PanelConfig gained "Save to contact field"
> per variable, with the sidebar showing contact fields above a section now
> labelled "Session data".
>
> Verifying S7 in a browser surfaced three further pre-existing bugs, all fixed:
> the automations list read the `{status, data}` envelope directly and was
> **permanently empty** however many rules an org had — the detail view, action
> catalog and run log had the same bug; every automation trigger rendered as a
> raw identifier, because vue-i18n reads the dots in `contact.created` as
> nesting while the locale file stored those keys flat; and `<CrmActionList>`
> was briefly used without being imported, which renders nothing and only warns,
> so the typecheck passed. The e2e check now fails on console warnings for that
> reason.
>
> **S9 (access scope)** is done for the part that was breaking users: visibility
> is now contact ∪ conversation. The inbox's Unassigned view lists conversations
> nobody has taken, but the contact scope only allowed contacts an agent owned
> or held a transfer for — so an agent saw a queued conversation and got a 404
> opening it, which reads as the product being broken rather than as a
> permission boundary. Both the handler scope and `contactquery.Scope` now
> include the general queue and the agent's own team queues, and stop there: a
> bot-held conversation and another team's queue remain hidden.
>
> **S8** gained a team-delete guard. Deleting a team left its transfers and
> conversations pointing at a team that no longer existed — they vanished from
> every team view without being reassigned. Delete is now refused with 409 and
> the dependent counts, or moves the work when `reassign_to_team_id` is given;
> resolved work names its team historically and does not block.
>
> Two suite failures that had been written off as "time-dependent flakiness"
> turned out to be the same class of bug and are fixed. A chatbot timing test
> read the weekday from the server's local clock while the engine evaluates
> schedules in the organization's zone, so for the five hours a day when the
> server's date runs ahead the schedule named tomorrow and the flow correctly
> routed out-of-hours. And `customfields.Coerce` normalised a date given as a
> string but not one given as a `time.Time` — the column is a bare date, so
> Postgres truncated it in whatever zone the session used, and a renewal set
> from a machine five hours ahead of UTC landed on the previous day, firing its
> reminder a day early with nothing recording that the day had moved. Only the
> Windows symlink test still fails locally, and that is a privilege the sandbox
> does not have rather than a defect.
>
> **S11 (time and formatting) is done.** Two settings had existed since before
> the CRM and were applied nowhere.
>
> The organization timezone offered five zones — UTC, New York, Los Angeles,
> London, Tokyo — so anyone outside them had to pick the wrong one, and that
> wrong zone then decided their business hours, SLA windows and every report
> range. The picker is now a searchable list of the browser's full IANA
> database, each entry showing its current UTC offset because "Asia/Thimphu"
> means nothing to most people and "UTC+06:00" does. Agents can override it for
> themselves, which is what someone working from another country needs; the
> server rejects a zone it cannot resolve rather than storing one that would be
> silently ignored.
>
> `date_format` was written to the database and read by nothing: every date in
> the product went through two helpers that hardcoded `'en-US'` and the
> browser's own timezone, so a team in Mumbai read American dates and two agents
> in different countries could disagree about what day a message arrived. The
> helpers now take the viewer's zone, the org's date format and the UI language
> — switching the interface to French switches the month names too. They are
> held as a module singleton that the auth store pushes into, rather than
> threaded through the thirty-odd files that call them from outside a Vue setup
> context; `useFormatters()` is there for components that want reactivity.
>
> API date ranges are also interpreted in the organization's timezone now. A
> date has no instant of its own, and these were parsed as UTC while the date
> picker that produced them built them from the browser's local calendar — so
> "today" meant two different windows at each end of the request, quietly moving
> several hours of activity into the wrong day.
>
> **S12** took its navigation item. The dashboard kept its own hand-written list
> of shortcut destinations, never updated for anything the CRM added: Contacts,
> Tasks, Pipeline, Segments, Automations, Reports, the Inbox and the audit log
> could not be pinned at all, and its "Contacts" tile went to the settings page
> while the sidebar's went to the contact list. The catalog is derived from
> `navigation.ts` now, with the old keys kept as aliases so shortcuts people had
> already saved still resolve.
>
> **S10 (realtime) has its correctness and privacy half.** `new_message` was
> broadcast to the entire organization with the contact's name and the message
> body in the payload, so every connected client received every customer
> conversation in the org — a support agent who could not open a contact in the
> list still had that contact's messages arriving in their socket, and the same
> feed drove the toast notifications, so the leak was on screen. Delivery now
> goes through the S9 rules, computed in three queries regardless of how many
> clients are connected. Three more, all found in the same pass:
>
> - The toast targeted the **contact owner**. An owner is a relationship, often
>   a salesperson; the person who needs to know a reply landed is the agent the
>   conversation is assigned to. The payload now carries the conversation
>   assignee and the owner is the fallback.
> - Every inbound message called `fetchContacts()`, which refetches page 1 and
>   replaces the array — so an agent who had scrolled the inbox was thrown back
>   to the top each time a message arrived anywhere in the org. One row is
>   refreshed instead, and a row that is not in the list is only inserted when
>   no filter is active.
> - `currentContact` was written on the client's read goroutine and read on the
>   hub's broadcast goroutine with no synchronisation — a data race on a pointer,
>   hit whenever an agent switched chats under load. It is behind a lock now.
> - A full client send buffer dropped the message and logged a line. The client
>   went on believing it was up to date and nothing corrected it until the agent
>   happened to reload. It now gets a `resync_required` notice, and because that
>   notice would not fit either — the buffer being full is why we are there — one
>   queued message is evicted to make room. That is not a loss: the client is
>   already missing messages, and "refetch everything" supersedes whatever was
>   waiting. Note events are also filtered client-side to the open contact; the
>   singleton notes store was appending notes a colleague wrote on a different
>   customer to whatever panel happened to be open.
>
> Topic subscriptions (`subscribe`/`unsubscribe` replacing `set_contact`) remain
> the open half of S10, along with the per-store realtime dispatcher.
>
> **S9 is now complete too.** Beyond the visibility fix above, three things:
>
> *Scope reached the endpoints that had skipped it.* The notes list and the
> contact timeline were gated on `chat:read` and an id, nothing else — so any
> agent could read the internal notes and the entire history of any contact in
> the organization, including the ones the contact list deliberately refuses to
> show them. Both now go through the same scope as the contact itself, and
> creating a note on an unreachable contact is refused rather than silently
> filed. The notes cursor was also reading a note by id with no org check.
>
> *Masking became a property of the setting rather than of each handler.*
> Masking was applied handler by handler and every list written afterwards
> simply did not know about it: the deals list and board, the duplicate-contact
> list and the campaign audience preview all returned full phone numbers to
> organizations that had turned masking on. A setting that holds in some lists
> and not others is worse than no setting, because it is trusted. The stored
> campaign recipients are deliberately not masked — that is the send list, not a
> view of it.
>
> *The last hardcoded catalog went.* The audit log's resource filter was a list
> in the Vue component: ten entries against the twenty-nine the server writes,
> so changes to accounts, roles, webhooks, canned responses, tasks, deals,
> pipelines, segments and every settings section were recorded and then
> unfindable — and one of its ten was never written at all, so picking it always
> returned nothing. It is now served from `internal/audit`, grouped, with a test
> that parses the actual `logAudit` call sites and fails in either direction.
> Writing that test found a split nobody had noticed: contact edits were logged
> as `contact` while a contact merge was logged as `contacts`, and campaigns
> split the same way, so filtering for contacts returned the edits and hid the
> merges. The handlers now write one spelling and a migration moves the history
> onto it.
>
> **S8 is now complete.** Three gaps closed this pass:
>
> *User deactivation and removal.* Turning a user off changed one boolean.
> Their conversations stayed assigned to an account that could no longer sign
> in, so those conversations appeared in neither the Unassigned view nor any
> active agent's list and the customer waited on somebody who was gone; their
> API keys kept authenticating, because a key checks its own hash and never
> reads `users.is_active`; their team membership kept feeding round-robin; and
> their private segments became visible to nobody and editable by nobody.
> `entityrefs.ReleaseUser` now runs the plan's table in one transaction from
> both deactivation and org removal, and the rows the plan says to keep —
> contact, task and deal ownership — are deliberately left alone, because an
> owner is a relationship record and blanking it loses history an admin may
> want to reassign deliberately. Reactivation is not a deactivation and does
> nothing.
>
> *Template dependencies.* Meta moves templates out of APPROVED on its own
> schedule and nothing noticed: a campaign scheduled for the next morning woke
> up, materialised its audience and failed every recipient, and the first
> anyone knew was an all-red report with the audience already spent. Campaigns
> in draft/scheduled/queued are now paused and their owners notified, on both
> the status webhook and template deletion. A processing campaign is left
> alone — Meta has accepted those sends, and pausing mid-flight would strand
> half an audience.
>
> *Org purge.* `wacrm org purge <id>` deletes an organization and everything
> scoped to it, with `-dry-run` and a confirmation that makes the operator type
> the org name. Deleting the `organizations` row alone had left every other
> table behind, scoped to an id that resolved to nothing — invisible to every
> list query and deletable by nothing. The table list is discovered from
> `information_schema` rather than registered: for reference *checks* a
> registry is right because a missing entry fails loudly, but for a purge the
> unregistered table is precisely the data that silently survives. Two things
> the tests found rather than the design: foreign keys between org-scoped
> tables mean there is no static delete order, so blocked tables are retried
> under savepoints until a pass makes no progress; and a user who belongs to a
> second organization has to be re-homed onto it first, since `users` points at
> `organizations` and leaving them here blocks the purge outright.
>
> **Acceptance audit of plans 01-09.** The 58 criteria were checked against the
> code rather than assumed from the views existing. Eight gaps were found and
> closed: a new contact had no lifecycle stage; `{{contact.fields.*}}` was not
> available to custom actions; `contact.updated` did not exist as a webhook
> despite the catalog claiming it carried `field_changed`; the webhook picker
> was hand-maintained and offered 7 of 26 deliverable events, so every
> conversation, task and deal webhook was unsubscribable; a merged contact's
> URL did not resolve to the survivor; the pipeline module could not be
> switched off; the timeline returned deal and task history to viewers without
> those permissions; and the dashboard had no CRM data sources and no funnel or
> leaderboard display type. Two pre-existing widget filter fields
> (`contacts.is_read`, `transfers.source`) were offered in the picker but
> dropped at query time, and are now whitelisted.

| # | Issue | Evidence | Impact | Fix owner |
|---|---|---|---|---|
| X1 ✅ | **[FIXED — `619a6f1`; verified: an agent now gets 403 on webhooks/roles/campaigns/api-keys/audit-logs, and `custom-actions` returns the list with `config` redacted]** **Server-side permissions are missing in most handlers.** `campaigns.go`, `templates.go`, `webhooks.go`, `custom_actions.go`, `canned_responses.go`, `roles.go`, `messages.go`, `flows.go` contain no `requireAuth` / `requirePermission` / `HasPermission` call; the route-level RBAC hook is a no-op (`cmd/wacrm/main.go:568-579`). Verified: the demo **agent** (no webhooks/roles/campaigns permissions) gets `200` on `GET /api/webhooks`, `/api/roles`, `/api/campaigns`, `/api/custom-actions`. `CreateRole`/`CreateWebhook`/`StartCampaign` check only authentication + org | Any authenticated user or API key can read and very likely create webhooks/roles and start campaigns. Every permission table in plans 00–09 assumes enforcement that does not exist | S1 |
| X2 ✅ | **[FIXED — `619a6f1`; `cachedWebhook` keeps the secret, encrypted, across cache hits]** Webhook signing secret lost on cache hits → unsigned deliveries | `models.go:262`, `cache.go:268-290` | Receivers cannot verify events | F11 |
| X3 ✅ | **[FIXED — the campaign worker now calls `RecordOutbound` and links `messages.conversation_id`; tests in `campaign_conversation_test.go`]** Campaign worker writes `Message` rows directly, bypassing `SendOutgoingMessage` (no contact `last_message_at`, no WS, no `message.outgoing` webhook, no conversation hooks) | `worker.go:161` | 03 conversation hooks and F2 events would miss every campaign send | S4 |
| X4 🔎 | **[FIXED — `internal/contacts` lifecycle service refuses to restore a user-deleted or merged contact]** Every inbound message, reaction, echo, call **and campaign send** restores a soft-deleted contact (also overwriting tags/metadata on manual re-create) | `contactutil.go:30,44,65`, `contacts.go:1362-1386` | Deletes and merges are not durable | S2 |
| X5 🔎 | **[FIXED — `internal/entityrefs` cascades a rename across all 18 name columns in one transaction, on both the edit and re-connect paths; a test asserts the registry covers the live schema]** WhatsApp accounts are referenced by **name** in ~15 tables; renaming an account (`accounts.go:209`, also on re-connect `:772-806`) has no cascade; the worker looks accounts up by name | `models/*.go`, `worker.go:85` | Campaigns fail and history detaches after a rename; new plans copy the pattern | S8 |
| X6 🔎 | **[FIXED — the worker fails the recipient permanently instead of dereferencing a nil template]** A template deleted during a campaign makes the worker dereference nil | `worker.go:119,260`, `templates.go:537-551` | Worker panic | S8 |
| X7 🔎 | **[FIXED — partial unique index `idx_agent_transfers_one_active`]** "One active transfer per contact" is count-then-insert with no unique index; inbound webhooks run in goroutines | `agent_transfers.go:426,1221`, `webhook.go:329` | Two active transfers → ambiguous conversation assignee (03) | S5 |
| X8 🔎 | **[FIXED — table widgets mask contact-derived labels and phone columns; verified live with masking on]** Dashboard table widget returns raw `phone_number` ignoring phone masking | `widgets.go:1355-1383` | PII leak where masking is enabled | S9 |
| X9 🔎 | **[FIXED — `ExecuteCustomAction` requires `chat:write`, and values are encoded per destination (JSON/query/header); tests in `custom_actions_escape_test.go`]** Custom action execute has no permission/contact-scope check and interpolates values into JSON bodies without escaping | `custom_actions.go:270-305,600-632` | Data exposure, broken/injected payloads | S1, S6 |
| X10 🔎 | **[FIXED — rename/delete build JSON with `to_jsonb`/`jsonb_build_array`; regression tests confirmed failing against the old code]** Tag rename/delete builds JSON literals by string concatenation | `tags.go:176` | Tags with quotes break updates | S8 |
| X11 🔎 | **[FIXED — `tags:import`/`tags:export` seeded, and the admin system role is now topped up with newly added permissions so this cannot recur]** `tags:import/export` permissions are never seeded (import/export for tags cannot pass checks) | `import_export.go:109,170`, `models/roles.go:178-181` | Broken feature; same trap for tasks/deals export | S1 |
| X12 🔎 | **[FIXED — `internal/safehttp` is the one SSRF-safe client; IVR `http_callback` validates and dials through it]** IVR `http_callback` uses a plain `http.Client` (not the SSRF-safe client) | `calling/http_callback.go:36` | SSRF from IVR flows | S7 |
| X13 🔎 | **[FIXED — one `schedule.IsOpen` using the org timezone]** Business-hours checks in 4 places with different boundary rules (`<=` vs `<`) and server-local time | `chatbot_processor.go:1633`, `chatbot_graph_runner.go:554`, `calling/ivr.go:540`, transfer helpers | Inconsistent routing; F9 timezone ignored | S11 |
| X14 🔎 | **[FIXED — completion message, `on_complete_action`, cancel keywords, excluded numbers and session timeouts are all executed now; tests in `chatbot_flow_settings_test.go`]** Flow settings `on_complete_action`, `completion_message`, `cancel_keywords`, `timeout_message` are saved but the v2 runner never runs them; session `timeout` status is never written; AI context `trigger_keywords` ignored; `ExcludedNumbers` never read | `chatbot.go:910-915`, `chatbot_processor.go:663,859`, `models/chatbot.go:110` | UI promises behaviour that does not exist; 02/08 plans relied on `timeout`/completion | S7 (flow_completed event), 03 |

---

## 3. The integration spine (12 decisions)

Each decision states what changes, why (evidence) and which plan text it replaces.

### S1 — Enforce permissions on the server (Phase 0, P0)
- **Handler audit:** add `requireAuth(r, resource, action)` to every handler in the X1 files, following `tags.go` / `canned_responses.go`, with a **permission test per route** (403 for an agent role lacking the permission).
- **Route ↔ permission table:** introduce `routePermissions` in `main.go` (method + path pattern → resource:action) used by a startup self-check that fails CI if a registered `/api` route has neither a table entry nor an explicit `public`/`self` marker. This prevents regressions as plans add ~120 endpoints.
- **API keys:** set a distinct context key `auth_method=api_key` (`middleware.go:229-237`), so `actor_type=api` and `source=api` (01) work. API keys inherit the creator's **current** org membership (re-checked per request, not cached forever).
- **Catalogs:** permission catalog entries gain `group` and `label_key`, so `PermissionMatrix` groups new resources by area instead of a flat alphabetical list of raw names (`stores/roles.ts:47-63`, `lib/constants.ts:31-45`).
- **Backfill scope (F1 correction):** grant new permissions to system roles **and** to custom roles that already hold the closest existing permission (e.g. roles with `contacts:write` get `tasks:write`), recorded per grant so admins can review. Grants that a super admin explicitly removed are never re-added (track removals in `role_permission_revocations`).

### S2 — One Contact Lifecycle service (Phase 0)
`internal/contacts/lifecycle.go` replaces `contactutil.GetOrCreateContact`, the raw create in send-by-phone, the reflection importer for contacts, and the ad-hoc lookups (marketing preference, call-permission reply).
```go
Resolve(ctx, Identity{Phone, BSUID}, ResolveOpts{CreateIfMissing, AllowRestore, Source, Actor, ProfileName, UpdateName bool}) (contact, Outcome /*found|created|restored|followed_merge*/, error)
Delete(ctx, contactID, reason /*user|address_book_sync*/, actor)
Restore(ctx, contactID, actor)
```
- **Lookup chain (F7 + 06):** exact phone → `phone_normalized` → `contact_identities` → follow `merged_into_id`.
- **Restore policy:**
  - automatic restore only for `address_book_sync` deletions and inbound customer messages
  - **never** from campaign sends, reactions, echoes or calls
  - user-deleted contacts that write again create a *new* contact with a `restored_candidate` duplicate flag (06)
- **Profile name:** campaign CSV names no longer overwrite `profile_name` (`UpdateName=false` for the worker).
- **Events:** `contact.created` / `contact.restored` / `contact.deleted` for every path (today only inbound and address-book add emit `contact.created`).
- **Source (01):** auto-population happens inside `Resolve` from `ResolveOpts.Source`.
- **Delete cascade rules:**

| Table | On contact delete (reason = user) | On address-book-sync delete |
|---|---|---|
| conversations | resolve active, `resolution_reason=contact_deleted` | no change |
| agent_transfers | expire active | no change |
| chatbot_sessions | cancel active | no change |
| tasks | keep; excluded from lists/notifier until restore | no change |
| deals | keep; hidden from board until restore | no change |
| automation time triggers / segments (F6) | excluded via `contacts.deleted_at is null` | same |
| contact_identities | keep; lookups skip deleted targets unless restore allowed | same |

### S3 — Transactional outbox instead of in-process `PublishTx` closures (replaces F2 mechanism)
- **Why:** only 7 code paths use transactions today; inbound save, send and transfer create are multi-statement and non-transactional; audit and webhooks are fire-and-forget goroutines (`audit.go:147`, `webhook_dispatch.go:67`). After-commit closures are lost on crash, and the worker has no `App`.
- **Design:**
  - `crm_event_outbox (id, organization_id, type, contact_id, subject, actor, origin, data, occurred_at, published_at null)` is written in the same DB transaction as the change, together with `contact_activities` (F3).
  - A relay (F4 job running continuously, `SELECT … FOR UPDATE SKIP LOCKED LIMIT 500`) fans out to:
    - Redis stream `wacrm:crm_events` (automation)
    - Redis channel `wacrm:ws_fanout` (all replicas)
    - webhook delivery (with `webhook_deliveries` log, F11)
  - It then marks rows published. Published rows are retained for 7 days.
  - Event envelopes gain `event_id` and `delivery_id` (webhook payload today has neither, `webhook_dispatch.go:18-23`).
  - The worker, the calling manager (no `App`, `calling/session.go:137-148`) and scheduler jobs publish by inserting outbox rows — they only need the DB.
- **Code that must use it:**
  - existing `a.DispatchWebhook` call sites
  - the new events
  - `audit` writes for CRM entities (moved in-tx for durability)

### S4 — Messaging spine (Phase 0)
- **`messages.sender_type`** (`contact|agent|bot|automation|campaign|system|api|echo`) + `sender_user_id` (existing `sent_by_user_id`). Rules:
  - `first_response_at`, `last_agent_message_at`, "no agent reply" (08) and agent analytics count **only `agent`**, and only after the send succeeds
  - SLA warnings, out-of-hours replies and client-inactivity messages are `system`
  - API template sends are `api`
  - WhatsApp Business app echoes are `echo`
  - today all of these are indistinguishable (`SentByUserID` is set for API keys, `middleware.go:231`)
- **All outbound paths go through `messaging.Service` (F10):**
  - agent UI, template API, chatbot, SLA and out-of-hours
  - automation
  - **campaign worker (fixes X3)**
  - echo ingestion calls `messaging.RecordEcho`
- **Counters without double counting:** a DB trigger on `messages` INSERT (created in `getIndexes`) maintains `conversations.message_count`, `last_customer_message_at`, `last_agent_message_at`, `last_message_at` by `conversation_id` and `sender_type`. It covers worker, echo and any future path. `conversation.Service` owns *state transitions* only.
- **Status webhook ordering:** the worker inserts the message row **before** calling Meta, like `SendOutgoingMessage`, so status updates arriving first are not dropped.
- **Missing inbound paths:**
  - call-permission replies (`webhook.go:294-310`) and reactions call `conversation.Service.TouchInbound` (updates `last_inbound_at`, reopen rules) without creating a message row
  - BSUID-only messages (`webhook.go:321`) resolve contacts via S2 identities instead of being dropped

### S5 — Conversation / transfer / contact consistency (replaces 03 §4.3–4.4 mechanics)
- **Serialisation:**
  - `pg_advisory_xact_lock(hashtext(contact_id))` at the start of `conversation.Service`, transfer create/assign/resume, and merge (06)
  - **canonical lock order:** contact advisory lock → conversation row → transfer row (today `PickNextTransfer` locks transfer → contact, `agent_transfers.go:888,964`)
- **Integrity:** unique partial index on `agent_transfers (organization_id, contact_id) where status='active'` (fixes X7).
- **`internal/transfers.Service`** extracted from HTTP handlers (`ResumeFromTransfer` logic is HTTP-only, `:652`).
  - Every operation returns a typed result: `created | assigned | queued | suppressed_out_of_hours | already_active`.
  - Operations: `CreateAgentTransfer`, `saveAndFinalizeTransfer` callers, `PickNextTransfer`, `ReturnAgentTransfersToQueue`, SLA expire, resume.
  - Automation (08) records real outcomes instead of false success; the chatbot handoff and the inbox assign control show them.
- **Handoff state:** `conversations.handling` (`bot | human | handoff_pending | none`) replaces the boolean `bot_active`.
  - `handoff_pending` = a handoff was requested but suppressed out of hours / no agent available. Such conversations appear in **Unassigned** with an "Out of hours" chip, and "no agent reply" (08) covers them.
  - Derived from real state: active transfer → human; active chatbot session → bot; suppressed handoff → handoff_pending.
- **Contact owner is never cleared by transfers.** Today unassign/return-to-queue clears `contacts.assigned_user_id` (`agent_transfers.go:790-797,1400-1406`); this contradicts the owner/assignee split and breaks IVR owner routing. Owner changes only via explicit owner actions, which emit `contact.assigned`.
- **Resolve closes the bot session too:** resolve/auto-close ends `chatbot_sessions` (`status=completed|cancelled`), so the next customer message is not swallowed by a stale prompt node.
- **Reopen after resume:** conversations that were handed back to the bot and go quiet are resolved by a conversation-level idle timeout (`inbox.auto_resolve_idle_hours`), independent of SLA being enabled (today inactivity runs only when SLA is on, `sla_processor.go:94`).
- **Read state:** add `conversation_reads (conversation_id, user_id, last_read_message_at)`.
  - Opening a chat marks read **for that user**. WhatsApp read receipts are sent only when the viewer is the assignee (or no assignee and the viewer has `chat:write`).
  - Supervisors browsing All/Unassigned get a **peek** mode that does not mark read.
  - Unread badges use per-user state.
- **`CurrentConversationOnly`** (`contacts.go:288-301`) switches from chatbot session start to `conversations.opened_at`.

### S6 — One variable namespace, one renderer, one CRM context builder
- **Today, four syntaxes:**
  - canned responses: `{{contact_name}}`, resolved in the browser (`ChatView.vue:176,838-844`)
  - custom actions: `{{contact.name}}`, server-side (`custom_actions.go:576-632`)
  - chatbot session vars: `{{var}}` (`template_engine.go`)
  - plans used both `field.<key>` (05) and `contact.fields.<key>` (01/08)
- **`internal/crmcontext.Build(orgID, contactID, opts)`** builds, lazily and once per inbound/call/run:
  - `contact.{id,name,phone_number,tags,fields.<key>,owner.{id,name,email},lifecycle_stage,source,created_at}`
  - `conversation.{id,status,assignee.name,team.name,opened_at}`
  - `user.*` (acting user), `org.{name,timezone}`
  - `event.*`, `task.*`, `deal.*` when applicable
  - `now`
- **`internal/render`** wraps `processTemplate` with:
  - the namespace (reserved roots `contact.`, `conversation.`, `user.`, `org.`, `event.`, `task.`, `deal.` are read-only; chatbot session vars can never be written under them)
  - context-aware escaping (`text`, `json`, `url`)
  - phone masking where required
  - a `GET /api/variables?context=canned|campaign|automation|chatbot|custom_action` catalog for UI variable pickers
- **Consumers:**
  - canned responses (server-side preview/resolve; legacy `{{contact_name}}` aliases kept)
  - chat template parameter prefill
  - campaign `param_mappings` (05)
  - automation templates (08)
  - chatbot messages and conditions — the expr environment gets `contact`/`conversation` too, fixing "conditions see only session data" (`chatbot_graph_runner.go:120,487,502`)
  - keyword rule bodies
  - AI context static content
  - custom actions (fixes X9 escaping)
  - IVR variables (flattened strings)
  - webhook `call_webhook` bodies
- **`param_mappings` keys use template parameter names** (`ExtParamNames`, positional names are `"1"`, `"2"`…), with header/body/button sections separate, matching recipients' `template_params` (`CampaignDetailView.vue:846-869`, `worker.go:260-269`). This corrects 05's numeric-only shape.

### S7 — One CRM action library shared by automation, chatbot, keyword rules, slash commands and bulk actions
- **`internal/crmactions`:**
  - one registry of `Action{Type, ConfigSchema, Validate, Execute}` with a run context (`actor: user|bot|automation|api`, `origin/depth`, `idempotency_key`)
  - idempotency keys: automation `rule+event`; chatbot `session+node+visit`; slash command `request id`
  - **Actions:** set_field / set_lifecycle_stage, add_tags / remove_tags, set_contact_owner, assign_conversation, set_conversation_status, create_task, add_note, create_deal / move_deal_stage, send_template, send_message, call_webhook (SSRF-safe client — also used by IVR `http_callback`, fixing X12), notify_users
- **One Vue action-card component set** (`components/crmactions/*`) used by the automation builder, the chatbot node properties panel, keyword rule detail and bulk-action dialogs.
- **Chatbot integration** (replaces 01's `save_to_field`-only approach):
  - **"CRM action" node:** runs one or more library actions, then continues on `default`, or on `error` when configured.
  - **"CRM condition" node:** F6 AST or segment membership, with `true`/`false` edges.
  - **`save_to_field` shortcut** on prompt, **buttons** and **WhatsApp Flow** nodes. WA Flow forms are the natural place to collect email/company (`chatbot_graph_runner.go:868-874`).
  - **api_call nodes:** get a separate `field_mapping: {field_key: json_path}`. Putting `contact.fields.x` inside `response_mapping` would be stored as a flat key and never read back.
  - **Simulation:** new nodes need entries in `useFlowGraphSimulation.ts`.
  - **Palette:** add the missing `ai_response` / `set_variable` palette items while touching it (`ChatbotFlowBuilderView.vue:127-137`).
  - **PanelConfig reconciliation:** flow `PanelConfig` session fields stay as "Session data". The panel config editor gains "Save to contact field" per variable, and the contact sidebar shows contact fields first.
- **Keyword rules stay synchronous** (not an automation trigger). Their responses gain templated bodies (S6), the existing but unused `flow` response type in the UI (`KeywordDetailView.vue:73,262-266`), and an optional action list from the library.
- **Automation (08) consumes the library** instead of defining its own executors. Automations default to **"skip while a bot session or flow is active"** for `send_*` and `assign_conversation`, so they never race the chatbot greeting (`agent_transfers.go:1204` cancels sessions mid-flow). Automation sends use `sender_type=automation` and **do not** arm chatbot client-inactivity reminders (`TrackSLA`, `messages.go:258`).
- **Duplicate nudges:** automation `time.no_customer_reply` and SLA client-inactivity reminders target the same moment. The rule builder warns when both are enabled, and inbox settings expose "Reminders handled by: Chatbot settings | Automations".
- **Flowgraph:** 08 stores action lists in a shape that maps 1:1 to a linear `flowgraph.Graph`, so v2 branching/waits can reuse `internal/flowgraph` as a third specialisation next to chat and IVR.

### S8 — Entity references, lifecycle and org seed/purge
- **`internal/entityrefs`:** every table that references a user, team, WhatsApp account, template, tag, field option, segment, pipeline/stage or automation registers a resolver.
  - **Delete/rename handlers call** `refs.Check(kind, id)` (409 with dependents list) or `refs.Reassign(kind, from, to)` / `refs.Detach(kind, id)`.
  - **The same registry drives:** 06 merge re-pointing (which also covers `automation_runs`, `automation_contact_state` with upsert on PK collision, `notifications.entity_id`), org purge, and the "broken reference" badges in automation/segment/campaign UIs.
- **Accounts by ID:** all **new** tables store `whatsapp_account_id uuid` (`conversations`, automation `send_template` config, F6 account filter values, report dimensions). Existing name-keyed tables keep names until a later migration; `UpdateAccount` rename cascades name changes in one transaction to every registered name reference (fixes X5).
- **Templates:**
  - campaigns and automations reference `template_id`
  - status webhooks (`webhook.go:502`) that move a template away from `APPROVED`, template edits that change parameter names, and deletes → pause dependent scheduled/draft campaigns, mark dependent automation actions broken, notify owners (F5)
  - the worker handles a missing template as a permanent recipient failure (fixes X6)
  - `StartCampaign` validates approval (`campaigns.go:437-443`)
  - the campaign template picker filters by account and approval (`ListTemplates` reads `account` but the UI sends `whatsapp_account`, `templates.go:70`)
- **Tags:** keep name-keyed storage on contacts but route rename/delete through `entityrefs`, which rewrites tag names inside segment ASTs, automation trigger configs and action configs with parameterised JSONB updates (fixes X10).
- **Users** (`users.go:507-628`, `organization.go:662` member removal):

| Reference | On deactivate / remove from org |
|---|---|
| active transfers | return to queue (`transfers.Service`) |
| `conversations.assignee_id` | unassign (→ Unassigned view) |
| `contacts.assigned_user_id` (owner) | keep, flagged "Owner inactive" in lists; bulk reassign dialog offered to the admin |
| `tasks.owner_id` | keep; appear in All view with "Owner inactive"; admin prompted to bulk reassign |
| `deals.owner_id` | same as tasks |
| `automation_rules.created_by_id` | ownership transfers to the acting admin; rule keeps running |
| automation configs with `user_ids` | action marked broken → skipped with reason, owner notified |
| private segments | converted to shared, owned by the acting admin |
| `team_members` | removed (today rows survive member removal) |
| API keys | revoked |

- **Teams:** delete blocked (409) while referenced by active transfers/conversations, automation configs or chatbot transfer nodes (`chatbot_flow_migration.go:327`), unless `reassign_to_team_id` is given.
- **Org seed:** one `orgseed.Seed(tx, orgID)` called by `CreateOrganization` (`organization.go:432-476`), `CreateDefaultAdmin` (`database/postgres.go:303-376`) and F1 migrations. It seeds roles, chatbot settings (the default org currently gets none), widgets, custom fields (01), task types (04), default pipeline (07) and inbox settings.
- **Org purge:** a registry-driven `orgpurge` (admin CLI command `wacrm org purge <id>`, no HTTP endpoint in v1) deletes all rows by `organization_id`, including new tables.

### S9 — Access scope, masking and catalogs
- **`contactquery.Scope(viewer)` (F6)** becomes **contact ∪ conversation** visibility: assigned contact, active transfer to viewer, **or** conversation unassigned in viewer's team/general queue. Today the inbox would list these but `GetMessages`/`GetContact`/`SendMessage` would 404 them (`contacts.go:208`).
- **All contact-bearing handlers** use it: timeline, tasks, deals, notes (the notes list currently has no contact-scope check, `conversation_notes.go:44`), custom actions, segment member lists, search, reports tables.
- **Phone masking helper:** `masking.Apply(viewer, value)`, **org settings cached** (today `ShouldMaskPhoneNumbers` hits the DB on every call including every broadcast, `organization.go:317`), required in every list/serializer introduced by plans 02–09 plus widgets (X8), transfers and call logs UIs.
- **Backend-driven catalogs** (no hardcoded UI lists): permissions (group/label), webhook events (`group`, `label_key`, payload example), audit resource types and actions (today a hardcoded list of 9, `AuditLogsView.vue:165-173`), automation triggers/actions, filter fields.

### S10 — Realtime model v2 (replaces per-plan WS details)
- **Visibility-safe payloads:** events carry **ids plus minimal non-sensitive fields** (status, counts, timestamps). Clients refetch details with their own permissions. Where content is needed (new message preview), the relay targets `BroadcastToUsers(visibleUserIDs)` computed by S9 scope instead of org-wide broadcast (today `new_message` and transfer events leak names/previews org-wide, `messages.go:542-553`).
- **Subscriptions:** replace single-valued `set_contact` with `subscribe {topics: ["contact:<id>", "conversation:<id>", "board:<pipeline>"]}` / `unsubscribe`. This fixes:
  - `BroadcastToContact` reaching idle clients (`hub.go:155`)
  - notes from other contacts entering the singleton notes store (`websocket.ts:276-284`)
  - profile page + chat split views
- **Hub correctness:** `currentContact` data race (`client.go:269`), silent drops when the channel is full (`hub.go:175` → log + resync hint).
- **Frontend handling:**
  - one `realtime` dispatcher that routes by topic to stores
  - handlers for `contact_updated`, `contact_merged`, `conversation_updated`, `task_updated`, `deal_updated`, `notification_created`, `custom_fields_updated`
  - list patching respects the active view's membership rules (status/team/tags/account); when unsure, refetch the single row
  - `new_message` no longer triggers `fetchContacts()` (`websocket.ts:375-377`)
  - **toast targeting:** `new_message` toasts go to the **conversation assignee** (fallback: owner), not the contact owner (`websocket.ts:334`)
  - transfer/call toasts deep-link to `/chat/:contactId` instead of `/chatbot/transfers` or `/calling/logs` (`websocket.ts:431,473,503,520`)

### S11 — Time, schedules and formatting
- **F9 correction:** org `timezone` and `date_format` **already exist** (`organization.go:29-94`, `SettingsView.vue:283-306`), with only 5 hardcoded zones and `date_format` unused. F9 becomes: full IANA searchable select, per-user override, and actually applying both.
- **Date range semantics:** API `from`/`to` dates are interpreted in the **org timezone** everywhere (`useDateRange` builds browser-local dates while `parseDateRange` assumes UTC, `helpers.go:121-133`).
- **`internal/schedule`:** one business-hours/schedule evaluator (inclusive start, exclusive end, org timezone) used by chatbot settings, flow timing nodes, IVR timing, transfer helpers, automation time triggers, snooze presets and task due buckets (fixes X13). Business hours gain an explicit timezone (default org timezone).
- **Frontend:** `useFormatters()` (auth-store timezone + i18n locale + `date_format`) replaces `lib/utils.ts` formatters (hardcoded `'en-US'`), `ChatView.formatMessageTime`, dashboard relative time; uses the unused `time.*` i18n namespace.

### S12 — Frontend platform (shared UI building blocks)
| Building block | Replaces / unifies | Used by |
|---|---|---|
| **`ContactSidebar`** with pluggable ordered sections (Header, Conversation, Details/fields, Tags, Tasks, Deals, Notes, Duplicates, Automation runs, Additional data, Session data) | `ContactInfoPanel` + separate `ConversationNotes` panel + profile columns (01/02/04/07 each planned their own) | Chat side panel (sheet on mobile), contact profile columns |
| **Unified assignment control** (assignee chip menu: Assign to me / user / team, Return to queue, Hand back to bot; owner shown separately) | Chat "Assign" dialog (sets owner, role-name gated, `ChatView.vue:291-306,1204-1221`), "Transfer to agent" item (`:2010`), Resume button (`:1936,2014`) | Inbox header, inbox bulk bar, contact profile |
| **`EntityLink` / `ContactChip` / `UserChip`** (avatar, masked phone, permission-aware link, hover card) | Plain-text names in call logs, call transfers, agent transfers, ActiveCallPanel, audit logs, campaign recipients | Every list introduced by 02–09 + retrofits |
| **`RecordSheet`** shell (header, fields, related tasks, activity, "Open full page") | Separate Task/Deal sheets | 04, 07 (first uses of `components/ui/sheet`) |
| **`DataTable` v2** (`rowClick`, `selectable` + bulk bar slot, true `serverSort`, column visibility, `EmptyState`/`ErrorState` slots) | Current table (client-side sort even when emitting, no selection/row click, `DataTable.vue:84-115,203-216`) | Contacts, tasks, deals list, runs, reports |
| **Generic `FilterBuilder` + `useListViewState`** (URL state, saved views) in `components/shared` | Contacts-only builder in 01/05 | Contacts (segments), Tasks (Mine/Team/All + saved filters), Deals list, automation conditions, campaign audience |
| **`useShortcuts` registry + `?` sheet + `⌘K` command palette** (search contacts, conversations, tasks, deals, segments, automations, pages; respects input focus) | Per-plan shortcut sets (03 j/k/e/s/p, 04 n/x/r, 07 Ctrl+←/→) with collisions | Whole app |
| **Navigation single source** (`navigation.ts` drives sidebar, `navigationOrder`, dashboard shortcuts; items gain `badgeKey` and `feature` flag) | Three hand-kept lists (`navigation.ts`, `router/index.ts:344-376`, `DashboardView.vue:128-149`) that already drift | Sidebar badges (Chat unread, Tasks due), pipeline feature toggle, permission-aware shortcuts |
| **`NotificationBell`** placed next to `UserMenu` at the sidebar bottom (visible collapsed) and in the mobile top bar | F5's logo-row placement (hidden on mobile/collapsed, `AppLayout.vue:190`) | F5 |

---

## 4. Integration map by existing surface

### 4.1 Chat / Inbox (`ChatView.vue`, `stores/contacts.ts`, `websocket.ts`)
| Aspect | Integration |
|---|---|
| **Store** | Evolve `stores/contacts.ts` into the inbox store (add a conversation slice; keep `currentContact`, `messages`, `replyingTo`, `accountFilter`) instead of a parallel `stores/inbox.ts`. Callers of `fetchContacts()` (`websocket.ts:375,695`, `UserMenu.vue:100`, `ChatView.vue:237,1216,1259`) move to `refreshInbox()` |
| **List** | Views Mine / Unassigned / Bot / All + status; rows show `last_message_preview` (already returned by the backend, missing from the FE type), SLA chip, handling icon, snooze time, per-user unread |
| **Search** | Search falls back to **all contacts** (F6 `/contacts/search`) so contacts without a conversation (imported, older than the 03 backfill window) remain reachable; first agent send creates a Pending conversation |
| **Fields in the panel** | The chat panel loads `GET /contacts/{id}?include=fields,owner,conversation` on selection (list rows never carry fields, `ChatView.vue:561`) |
| **Thread** | **System event pills** in the message stream from `contact_activities` (status changes, assignments/handoffs, task created/completed, deal stage changes, automation actions, calls with outcome). A thread toggle "Show activity" keeps chat and timeline telling the same story |
| **Notes** | Notes become contact-level "Internal notes" shown both as a sidebar section and (optionally) inline as distinct note bubbles; `ConversationNote` gains nullable author + `author_type` so automation notes are valid (`conversation_notes.go:12` requires a user today) |
| **Header** | Priority order: name → `EntityLink` to profile (router guard must allow scoped agents via S9), status control (Resolve split button), unified assignment control, call, overflow menu (custom actions, notes, info, merge, create deal). On narrow widths secondary actions collapse into the overflow menu |
| **Composer** | Reserved **Commands** group in the existing `/` picker (`ChatView.vue:1192-1202`): `/task`, `/note`, `/deal`, `/snooze`, `/resolve`, `/assign`, backed by S7 actions; canned responses remain in their own group. Template picker prefills params via S6 and saved per-template mappings |
| **Message actions** | Hover menu gains Create follow-up (04), Add note about message, Create deal (07), Copy to field (01); touch devices get long-press action sheet |
| **Service window** | Client-side countdown from `last_inbound_at` (banner flips when 24 h passes while open); Pending rows show "Window closes in 3 h"; banner precedence: service window > snoozed > resolved |
| **Deep links** | `GET /contacts/{id}/messages?around=<message_id>|<timestamp>` so timeline bursts and notifications open at the right point (`contacts.go:308-371` cannot today); `sent_by`/`sender_type` exposed in `MessageResponse` so agent/bot/automation authorship is visible |
| **Duplicates** | "Possible duplicate" banner also in the chat header when a pending candidate exists (06) |
| **Calls** | `ActiveCallPanel` links to chat/profile and does not cover the composer; after a call ends: outcome dialog (answered/no answer/voicemail + notes) with "Schedule callback" (creates `call_back` task, 04) |

### 4.2 Transfers page (`AgentTransfersView.vue`)
Absorbed into the inbox:
- Unassigned view = queue with team filter and **Pick next** (existing logic, `AgentTransfersView.vue:177-199,321-344`)
- Mine = my handoffs
- SLA/History remains as a **Supervisor** tab under Inbox (`/chat?view=supervisor`) or Analytics
- `/chatbot/transfers` redirects
- the Chatbot nav loses its Transfers child

### 4.3 Chatbot, keyword rules, AI contexts
- **Flow builder** gains:
  - CRM action node, CRM condition node, `save_to_field` on prompt/buttons/WA Flow, `field_mapping` on api_call (S7)
  - variables `contact.*` / `conversation.*` in templates and expressions (S6)
  - `chatbot.flow_completed` event `{flow_id, variables}` (automation trigger, activity), replacing the dead `on_complete_action`
  - implementation of `completion_message`, `timeout_message`, `cancel_keywords` (X14)
- **Session timeout:** `chatbot_sessions` gain real timeout handling (the SLA/idle job marks stale sessions `timeout`).
- **Keyword rules:** templated bodies, flow response type, optional S7 actions (e.g. "add tag *Pricing interest*").
- **AI:**
  - built-in, opt-in **"Contact profile" context** (fields, tags, lifecycle, open tasks, open deals, conversation status) with a field allowlist for PII
  - static contexts rendered with S6 variables
  - `trigger_keywords` implemented or removed from the UI

### 4.4 IVR and calling
- **Context and events:**
  - IVR variables from `crmcontext` (flattened)
  - calling manager publishes via outbox (S3): `call.missed`, `call.completed`, `call.transfer_no_answer` (catalog additions; missed detection from `call_webhook.go:215-219,250,333-336`, separating outgoing-unanswered from inbound-missed)
  - calls creating contacts emit `contact.created` (today `isNew` is discarded, `call_webhook.go:103`)
- **IVR editor:**
  - condition node on contact fields/tags/lifecycle/segment (VIP routing)
  - explicit transfer target **Contact owner** (calls already ring the owner first implicitly, `calling/transfer.go:104-120`; surface it in `IVRNodeProperties.vue:415`)
- **Call records:** `call_logs` gain `disposition` + `notes`; `tasks.call_log_id` links callbacks; calls appear in timeline (02) and thread pills (4.1).
- **Automation:** triggers `call.missed` ("create call-back task for owner"), `call.completed`.

### 4.5 Campaigns and templates
- **Sending:** the worker sends through `messaging.Service` (S4) with `sender_type=campaign`, sets `recipient.message_id`, and resolves contacts with `ResolveOpts{AllowRestore:false, UpdateName:false}` (S2).
- **Attribution:**
  - `messages.metadata.campaign_id` is carried to the conversation (`conversations.origin_campaign_id`) when the customer replies within the attribution window (default 72 h)
  - `campaign.replied` event, "Replied" counter on the campaign, `last_campaign_id` / `replied_to_campaign` filter fields (F6), contact `source=campaign` on first touch
- **Audiences:** list and segment audiences both produce recipients with `contact_id`.
  - Unique partial index `(campaign_id, contact_id) where contact_id is not null and deleted_at is null` (the soft-delete omission in 05 would collide).
  - The worker treats index conflicts as a permanent "duplicate" failure so campaigns still complete.
- **Recipients UI:** `ContactChip` links and server pagination (`campaigns.go:741` returns all).
- **Templates:** S8 dependency guard (edit/delete/unapproval); `StartCampaign` validates approval; picker filters by account + approval.
- **Frequency cap** (org setting, optional): skip contacts that received a marketing template in the last N hours across campaigns.

### 4.6 Canned responses, custom actions, webhooks
- **Canned responses:** rendered server-side through S6 (`POST /api/canned-responses/{id}/render {contact_id}`) with the variable picker from `/api/variables`; legacy `{{contact_name}}` aliases remain.
- **Custom actions:** scope + permission checks (X9), S6 rendering with JSON/URL escaping, execution recorded as activity (`custom_action.executed`), fetched for every user who can execute them (today only admin/manager, `ChatView.vue:454`).
- **Webhooks:** grouped, validated event names from the catalog (`CreateWebhook` accepts anything, `webhooks.go:202`); payloads gain `event_id`/`delivery_id`; delivery log (F11); docs updated (`docs/.../features/webhooks.mdx` still says "Seven event types").

### 4.7 Dashboard, analytics, reports
- **Access:** agents cannot open the Dashboard (route requires `analytics`; agents lack it, `router/index.ts:43-46`). Add a permission-free **Home** (`/home`), the default landing page for users without `analytics`, with My conversations, My tasks due/overdue, recent notifications and quick create. Admins/managers keep the Dashboard.
- **Default widgets:** new defaults (open conversations by status, my tasks due/overdue, pipeline value, segment counts) are added to **existing orgs** by an F1 migration (`SeedDefaultWidgets` skips orgs with any widget, `postgres.go:673-679`).
- **Widget queries and pinning:** `executeWidgetQuery` receives the viewer (for "me" widgets and masking). "Pin to dashboard" (09) checks `reports:read` at **render** time, not only at pin time.
- **Shortcuts:** the registry is generated from navigation (S12), permission-aware.

### 4.8 Settings information architecture
| Setting | Decided location |
|---|---|
| Org timezone, date format, currency, default country code | Settings → General (existing card, extended) |
| Business hours, SLA, client inactivity, reopen window, auto-resolve, idle timeout, "reminders handled by" | **Settings → Inbox** (new page; SLA/Hours/Agents tabs move out of `/settings/chatbot`, which keeps Messages & AI) |
| Personal notification preferences, personal timezone, list column preferences | **Profile** (all users; today notification prefs sit in Settings → Notifications behind `settings.general`, unreachable for agents, `SettingsView.vue:387-430`); `UpdateCurrentUserSettings` (`users.go:715-750`) extended |
| Contact fields, task types, pipelines & stages | Module-local settings (gear in Contacts / Tasks / Pipeline headers) + listed on a Settings overview page; **not** added as Settings sidebar children |
| Automations kill switch | Automations page header |
| Feature toggles (pipeline) | Settings → General → Features |

### 4.9 Navigation (final)
| Section | Items | Notes |
|---|---|---|
| **Main** | Home (agents) or Dashboard, **Inbox** (renamed from Chat, unread badge), **Contacts**, **Tasks** (due badge) | |
| **Sales** | **Pipeline** | feature-flagged |
| **Messaging** | Campaigns, Templates, Flows | |
| **Automation** | Chatbot (Overview, Keywords, Flows, AI Contexts), **Automations** | Transfers child removed (4.2) |
| **Calling** | Call Logs, IVR Flows, Call Transfers | |
| **Analytics** | Agent Analytics, **CRM Reports**, Meta Insights | |
| **Settings** | existing children minus Contacts; plus **Inbox** | Module settings live inside modules |
| **Bottom** | Notification bell + user menu | |

### 4.10 Roles, audit logs, import/export, API
- **Roles UI:** grouped by catalog group with labels (S1/S9).
- **Audit logs:**
  - resource types and actions from the catalog (S9)
  - CRM entities audited in-tx (S3)
  - new actions beyond created/updated/deleted: `merged`, `assigned`, `started`, `imported`
- **Import/export:**
  - a dedicated contacts importer (S2 lifecycle, fields, events, validation of `assigned_user_id` / account)
  - the reflection importer remains for simple tables
  - export filters accept the F6 AST
  - `tasks:export`, `deals:export`, `tags:import/export` permissions seeded (X11)
  - the dialog sends `column_mapping`
- **Public API docs:** new pages under `docs/src/content/docs/api-reference/` for contact fields, conversations/inbox, tasks, segments, deals/pipelines, automations, notifications, reports; update `astro.config.mjs` sidebar and `features/roles-permissions.mdx` resource table.

---

## 5. End-to-end journeys (system acceptance)

Each journey must pass in e2e once its plans ship. Together they prove the features behave as one system.

**J1 — Campaign reply becomes a qualified lead**
1. A segment "Lifecycle = New, source = Import, not opted out" is targeted by a campaign with `{{customer_name}}` mapped to `contact.name` (05, S6).
2. The worker sends via `messaging.Service` (S4); the timeline shows the campaign send (02).
3. The customer replies: the conversation opens with `origin_campaign_id`, `campaign.replied` fires, and the campaign "Replied" counter increments (4.5).
4. Automation "Replied to campaign → set lifecycle Lead, create *Check in* task for owner" runs via S7; thread pills show both actions (4.1); the owner gets a notification (F5).
5. The agent resolves with "Resolve & add follow-up"; reports R1/R2 count the lead from the Import source (09).

**J2 — Missed call to scheduled callback**
- The IVR routes a VIP contact (condition node on tag) to the contact owner (4.4).
- No answer → `call.missed` → automation creates a `call_back` task due in 1 hour.
- The owner sees it in Home and Tasks.
- After calling back, the call outcome dialog completes the task.
- The timeline shows call → task created → call → task completed.

**J3 — Chatbot qualification writes CRM data**
- A WhatsApp Flow collects company and email and saves them to fields (`save_to_field`).
- A CRM condition node branches on "company is not empty".
- A CRM action node sets lifecycle Qualified and assigns to the Sales team (transfer outcome `queued`); the Unassigned view shows it with an SLA chip.
- `chatbot.flow_completed` is recorded.
- The AI context later answers using the stored fields.

**J4 — Agent goes away**
- The agent toggles away: `transfers.Service.ReturnToQueue` → conversations become unassigned (S5); contact owners are unchanged; tasks remain owned.
- Automations with "assign to user X" skip with reason if X is unavailable.
- Supervisor peek mode does not mark the chats read for anyone.

**J5 — Import, duplicate, merge**
- A CSV import with `0321…` numbers flags duplicates (06, S2).
- The admin merges from the chat header banner; the entityrefs registry re-points all tables, including automation runs and notifications.
- An inbound message from the old number lands on the primary; the timeline shows both histories; the old URL redirects.

**J6 — Account rename and user removal**
- An admin renames a WhatsApp account: the S8 cascade updates name references; scheduled campaigns and automations keep working.
- An admin removes a user:
  - transfers return to the queue
  - tasks/deals show "Owner inactive" with a bulk reassign prompt
  - that user's automations transfer ownership
  - the API keys are revoked

---

## 6. Plan corrections index

Each plan's *Integration addendum* lists these in context. Summary:

| Plan | Corrections |
|---|---|
| **README** | Add Phase 0 (this doc's S1–S12) and the new packages, events, WS topics and defects; update target navigation (4.9) |
| **00** | F2 → outbox (S3); F9 timezone already exists (S11); F1 backfill incl. custom roles + revocations (S1); F4 add outbox relay, session timeout, conversation idle jobs; F5 bell placement + prefs in Profile (S12, 4.8); F6 scope = contact ∪ conversation, masking (S9); F10 includes worker + echo + `sender_type` (S4); F11 event ids, validation, groups |
| **01** | Contacts importer + lifecycle (S2); chat panel loads fields (4.1); chatbot integration via S7 (CRM node, save_to_field on buttons/WA Flow, `field_mapping`), PanelConfig reconciliation; variables via S6 (`contact.fields.<key>` everywhere); `source=api` needs `auth_method` (S1); all old Contacts references (nav permissions, `navigationOrder`, dashboard shortcut, e2e `ContactsPage`); ContactSidebar sections (S12) |
| **02** | Profile uses ContactSidebar + WS subscriptions (S10); notes store not reused as singleton; `call_logs.contact_id` is non-null; `bot`/`api` actor types; chatbot `timeout` status requires S7/X14 fix; thread pills share renderers with timeline; deep link `around=` API; masking in serializers |
| **03** | Evolve contacts store; `handling` enum incl. `handoff_pending`; advisory lock + lock order + unique active transfer (S5); `transfers.Service` incl. `ReturnAgentTransfersToQueue`, `PickNext`, SLA expire; owner never cleared by transfers; resolve ends chatbot session; idle auto-resolve independent of SLA; per-user read state & peek; unified assignment control replaces assign dialog/Transfer/Resume; Transfers page absorbed; `CurrentConversationOnly` uses `opened_at`; search fallback to all contacts; counters via trigger + `sender_type` (S4) |
| **04** | `RecordSheet`; tasks from slash commands, message actions, call outcome (`call_log_id`); Home view for agents; user deactivation rules (S8); masking; nav badge via `badgeKey` |
| **05** | `param_mappings` keyed by template parameter names with sections (S6); unique index excludes soft-deleted; worker duplicate handling; campaign reply attribution & `campaign.replied`; template dependency guard; account ids; frequency cap option |
| **06** | Lookup chain lives in Contact Lifecycle (S2); re-point via entityrefs incl. automation runs/state (upsert), notifications; exclude `reason=merged` from automation triggers; merge uses S5 lock order; duplicate banner in chat header; worker must not resurrect merged contacts |
| **07** | `RecordSheet`; account ids not needed; owner inactive rules; board WS topic `board:<pipeline>` (S10); feature flag in nav source (S12); create deal from slash command/message action |
| **08** | Actions from the shared library (S7) — engine keeps triggers/policies/runs; `skip while bot active` default; `sender_type=automation` and no chatbot inactivity arming; duplicate-nudge warning; `createTransferToTeam` via `transfers.Service` typed outcomes; expr timeout wrapper; `add_note` needs nullable author; triggers add `call.missed/completed`, `chatbot.flow_completed`, `campaign.replied`, `conversation.sla_breached`; outbox consumer (S3); entityrefs broken-reference handling |
| **09** | Metrics use `sender_type=agent`; date ranges in org timezone (S11); viewer-aware widget queries + masking; reports check permission at render; Home widgets for agents; campaign reply rate report |

---

## 7. Revised roadmap

| Phase | Content | Effort (1 eng) |
|---|---|---|
| **0 — Platform spine** | S1 server-side permissions + route table self-check (**first, security**); F11 signing fix; S3 outbox; S4 messaging spine (worker/echo/sender_type/counters); S2 Contact Lifecycle; S5 transfers service + locks + unique index; S8 org seed + entityrefs skeleton + account-rename cascade + template guard; S11 schedule package + formatters; S12 DataTable v2, EntityLink/UserChip, nav single source, `useFormatters` | **5–6 w** |
| **1 — Record & conversation** | 01 (with S6 render + S7 library core actions for fields/tags), 03 (with S9 scope, S10 realtime v2, unified assignment control, Transfers absorbed, Settings → Inbox) | 6 w |
| **2 — CRM core** | 02 (ContactSidebar, thread pills), 04 (RecordSheet, Home), 05 (param names, attribution) | 7 w |
| **3 — Leverage** | 06 (entityrefs re-point), 08 (on S7 library; chatbot CRM nodes & condition node ship here too), IVR CRM integration (4.4) | 7.5 w |
| **4 — Sales & insight** | 07, 09, command palette + shortcuts registry | 6 w |

Phase 0 is not optional scaffolding. Items S1, S2, S4 and S5 fix defects that would corrupt or expose the data every later feature builds on. Building Phase 1 without them means re-doing the hooks afterwards.

---

## 8. Decisions needed from product

1. **Rename "Chat" to "Inbox"** in navigation once conversation status ships? (Recommended.)
2. **Home page for agents** (`/home`) as their default landing page? (Recommended; agents cannot see the Dashboard today.)
3. **Restore policy (S2):** should a customer who was *manually deleted* and writes again be restored (today's behaviour) or created fresh with a duplicate flag? (Recommended: create fresh + flag.)
4. **Read receipts:** send WhatsApp read receipts only when the assignee reads (recommended), or whenever anyone opens the chat (today)?
5. **Campaign reply attribution window:** 72 hours default?
6. **Who owns reminders:** keep chatbot client-inactivity reminders, or migrate them into automations over time? (Recommended: keep both with the warning in Phase 3; deprecate chatbot reminders after automation adoption.)
