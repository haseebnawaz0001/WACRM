# 02 — Contact Profile with a Timeline

| | |
|---|---|
| **Priority** | **P1** |
| **Phase** | 2 (first item) |
| **Effort** | 2 engineer-weeks |
| **Depends on** | 01 (Contacts module, fields, profile route), 03 (conversation history), F3 activity log, F6 access scope, F8 recipient `contact_id` |
| **Unlocks** | Host page for 04 tasks panel, 06 merge UI, 07 deals panel; realtime activity for 08 debugging |
| **Status** | Proposed |

## 1. Problem
Everything that happened with a customer already exists, but in separate places:
- **messages:** chat view
- **call logs:** Calling → Call Logs
- **conversation notes:** chat side panel
- **agent transfers:** Transfers page
- **chatbot sessions:** not shown
- **campaign sends:** campaign detail, by phone only
- **contact edits:** `audit_logs`, as a generic panel

No page tells the customer's story in order.

## 2. Goals
1. **Profile page:** one page per contact at `/contacts/:id` with a header summary, editable details (01), and a **unified, chronological timeline**.
2. **Timeline contents:** messages, calls, notes, tag changes, assignments, conversation status changes, transfers, chatbot sessions, campaign sends, field and lifecycle changes, and later tasks (04), deals (07) and merges (06).
3. **Timeline behaviour:** type filters, date range, infinite scroll (keyset), real-time appends, deep links to the original record.
4. **Mostly aggregation:** no duplication of high-volume data (messages, calls, notes stay in their tables).

### Non-goals
- A full message reader on the profile. Message bursts link to the chat.
- Editing historical items, except notes, which reuse existing note endpoints.
- Exporting the timeline (later).

## 3. Current state
| Source | Table / link to contact | Timestamp | Ref |
|---|---|---|---|
| Messages | `messages.contact_id` | `created_at` | `models.go:380` |
| Calls | `call_logs.contact_id` (nullable) + `caller_phone` | `started_at`/`created_at` | `call.go:46` |
| Call transfers | `call_transfers.contact_id` | `transferred_at` | `call.go:117` |
| Notes | `conversation_notes.contact_id` | `created_at` | `conversation_notes.go` |
| Agent transfers | `agent_transfers.contact_id` | `transferred_at`, `picked_up_at`, `resumed_at` | `chatbot.go:298` |
| Chatbot sessions | `chatbot_sessions.contact_id` | `started_at`, `completed_at` | `chatbot.go:216` |
| Campaign sends | `bulk_message_recipients.phone_number` (no contact_id → **F8** adds it) | `sent_at`/`delivered_at`/`read_at` | `bulk.go:45` |
| Contact edits | `audit_logs` (`resource_type='contact'`) | `created_at` | `models.go:517` |
| Tags/assignment/status/fields/tasks/deals | **not recorded anywhere today** → `contact_activities` (F3) | `occurred_at` | F3 |

Frontend today: `ContactDetailView.vue` (edit name/account/tags/assignee + `AuditLogPanel`), with `ContactInfoPanel.vue` in chat.

## 4. Design

### 4.1 Timeline item contract
```ts
interface TimelineItem {
  id: string                 // "<source>:<uuid>[:<suffix>]" — stable, unique
  type: TimelineType         // see table below
  occurred_at: string        // ISO; sort key
  actor: { type: 'user'|'system'|'automation'|'contact'|'api'|'bot', id?: string, name: string }
  summary: string            // server-rendered short text (English fallback); UI prefers i18n by type+data
  data: Record<string, any>  // type-specific payload for rendering
  link?: { route: string, query?: Record<string,string> } // deep link
  group?: { count: number, from: string, to: string }     // for message bursts
}
```

| `type` | Source | Rendering |
|---|---|---|
| `message_burst` | messages | Consecutive messages in one conversation with < 30 min gaps collapse into one item: "12 messages · 4 from customer", first and last preview, account, link `/chat/:contactId?conversation=` |
| `call` | call_logs | direction, status, duration, agent, IVR path summary, recording link if permitted |
| `note` | conversation_notes | full content, author; edit/delete for the author (existing endpoints) |
| `conversation_status` | activity (`conversation.*`) | "Resolved by Alex", "Snoozed until …", "Reopened" |
| `assignment` | activity (`conversation.assigned`, `contact.assigned`) | "Assigned to Priya (Support)" / "Contact owner changed" |
| `transfer` | activity (`transfer.*`) | "Transferred to Support queue (from flow)", "Picked up by …", "Handed back to bot", "Expired (SLA)" |
| `tag` | activity (`contact.tag_added/removed`) | "Tag VIP added"; consecutive tag changes by the same actor within 1 min merge into one item |
| `field_change` | activity (`contact.field_changed`) | "Company: Acme → Acme Ltd"; lifecycle stage gets its own type |
| `lifecycle_stage` | activity | coloured stage chips from → to |
| `chatbot_session` | chatbot_sessions | flow name, status (completed/cancelled/timeout), captured variables count, link to session data |
| `campaign_send` | bulk_message_recipients (F8) | campaign name, template, final status (sent/delivered/read/failed + error) |
| `contact_created` | activity (backfilled from audit) / `contacts.created_at` | source (01) |
| `task` | activity (`task.*`) — 04 | created / completed / overdue, links to task |
| `deal` | activity (`deal.*`) — 07 | created / stage change / won / lost |
| `merge` | activity (`contact.merged`) — 06 | "Merged with +92 321 …; 48 messages moved" |
| `opt_out` | activity (`contact.opt_out_changed`) | marketing opt-out on/off |

### 4.2 Aggregation algorithm
`GET /api/contacts/{id}/timeline?types=&from=&to=&before=<cursor>&limit=30`
- **Cursor:** opaque base64 of `(occurred_at, item_id)` of the last item returned.
- **Fetching:** for each enabled source, run one bounded query `WHERE contact_id=? AND ts < cursor_ts [AND ts >= from] ORDER BY ts DESC, id DESC LIMIT limit+1` (messages: `limit * 20` rows, then burst-grouped).
- **Merging:** k-way merge in Go, take `limit`, and return `next_cursor` if any source had more.
- **Burst grouping** happens after fetch. A burst crossing a page boundary is marked `partial: true` and continues on the next page (same stable id prefix; the UI merges).
- **Indexes needed:**
  - `messages (contact_id, created_at desc)`: add if missing; `idx_messages_contact` exists, extend it
  - `call_logs (contact_id, created_at desc)`
  - `conversation_notes (contact_id, created_at desc)` (exists)
  - `chatbot_sessions (contact_id, started_at desc)`
  - `bulk_message_recipients (contact_id, sent_at desc)` (F8)
  - activity index (F3)
- **Budget:** p95 < 300 ms for contacts with 50k messages. Each source query is index-only range scans limited to small N.
- **Implementation:** `internal/timeline` package with a `Source` interface:
  ```go
  type Source interface {
      Types() []string
      Fetch(ctx context.Context, q Query) ([]Item, bool /*hasMore*/, error)
  }
  ```
  Plans 04/06/07 register new renderers through the activity source (no new source needed); new tables with their own history would add a `Source`.

### 4.3 Permissions & privacy
- Access to a contact's timeline requires contact access via `contactquery.Scope` (F6): `contacts:read`, or assigned contact / active transfer.
- **Per-source gating:**
  - message previews need `chat:read`
  - calls need `call_logs:read` (recording links additionally need the existing recording permission)
  - campaign sends need `campaigns:read`
  - notes need `chat:read` (same as the notes API today)
  - sources the viewer cannot read are omitted silently, and `available_types` in the response lists what the viewer can filter on
- Phone masking settings apply to any phone shown in items (`utils.MaskIfPhoneNumber`).

### 4.4 Realtime
- The F2 realtime sink broadcasts `contact_activity_created` to clients viewing the contact (`BroadcastToContact`) with the rendered `TimelineItem`.
- New messages on a contact being viewed extend the newest `message_burst` locally (the profile subscribes via `set_contact` like the chat view).

### 4.5 Frontend
**Route** `/contacts/:id` → `views/contacts/ContactProfileView.vue` (replaces the Phase-1 detail view from 01). `stableKey: true` so switching tabs does not remount.

**Layout** (desktop, three regions; stacks on mobile)
```
┌───────────────────────────────────────────────────────────────────────────┐
│ ← Contacts   [Avatar] Mia Thompson   +1 415 555 0123   ● Customer         │
│              Owner: Priya · Tags: VIP Returning · Conversation: Open (Alex)│
│              [Open chat] [Log note] [Add task*] [Create deal*] [⋯ Merge*]  │
├──────────────────────┬──────────────────────────────────┬─────────────────┤
│ Details (01 fields)  │ Timeline                         │ Tasks* (04)     │
│  Email, Company…     │ [All ▾ types] [date range]       │ Deals* (07)     │
│  Additional data     │  ● Today                         │ Conversations   │
│  (metadata)          │   message burst …                │  (03 history)   │
│                      │   Tag VIP added by Priya         │ Segments* (05)  │
│                      │  ● Yesterday …                   │ Duplicates* (06)│
└──────────────────────┴──────────────────────────────────┴─────────────────┘
* rendered only when the owning feature is enabled/shipped and the user has permission
```
- **Header:** name (inline edit), phone (copy), lifecycle stage select (01), contact owner picker, tags editor, current conversation status chip (03, links to chat).
- **Details column:** `ContactFieldsForm` (01) in view mode with per-field inline edit; collapsible "Additional data" (metadata).
- **Timeline** (`components/contacts/timeline/`):
  - `ContactTimeline.vue`: filter chips per available type, date range (`DateRangePicker`), day separators, infinite scroll (`useInfiniteScroll`), "New activity" pill when realtime items arrive while scrolled down.
  - One small renderer component per type: `TimelineMessageBurst.vue`, `TimelineCall.vue`, `TimelineNote.vue`, `TimelineChange.vue` (tag/field/lifecycle/assignment), `TimelineTransfer.vue`, `TimelineCampaignSend.vue`, `TimelineChatbotSession.vue`. Registration is by type in a map, so 04/06/07 add renderers without touching the timeline.
  - The note composer is at the top ("Log a note") and reuses the notes store and API.
- **Right column:** extension panels registered by plans. Phase 2 ships **Conversations** (from `GET /api/contacts/{id}/conversations`, 03).
- The `AuditLogPanel` stays available under "⋯ → Audit history" for admins.
- **Entry points:** contacts list row click, chat header name click (new), notification links, global search result (future).

## 5. Migration & rollout
- F3 backfill must have run (contact created/updated from audit logs; transfer events).
- F8 backfill for campaign recipient `contact_id`.
- Index additions in `getIndexes` (idempotent).
- Ship behind the route switch: `/contacts/:id` renders the new profile; the old detail component is deleted after e2e parity.

## 6. Milestones
| # | Deliverable | Est. |
|---|---|---|
| M1 | `internal/timeline` sources (activity, messages w/ bursts, calls, notes, sessions, campaign sends) + k-way merge + cursor + unit tests | 3 d |
| M2 | Timeline endpoint with permissions gating + indexes + perf test on seeded data | 1.5 d |
| M3 | Realtime `contact_activity_created` + message burst extension | 1 d |
| M4 | Profile page layout, header, details column, conversations panel | 2 d |
| M5 | Timeline UI + renderers + filters + infinite scroll + note composer | 2.5 d |

## 7. Testing
- **Unit:**
  - burst grouping (gap boundary, conversation boundary, page boundary)
  - merge ordering with equal timestamps (tie-break on id)
  - cursor round-trip
- **Handler:**
  - an agent without `call_logs:read` gets no call items and `available_types` excludes `call`
  - an out-of-scope contact returns 404 (not 403, to avoid existence leaks, matching `findByIDAndOrg`)
- **Performance:** seed 50k messages + 2k activities on one contact; p95 of the first page < 300 ms locally.
- **E2E** (`e2e/tests/contacts/contact-profile.spec.ts`):
  - add a tag in chat → appears on the profile timeline in real time
  - log a note from the profile → visible in the chat notes panel
  - filter to Calls only

## 8. Risks & open questions
| Risk / question | Mitigation / proposal |
|---|---|
| Message volume dominates timeline | Burst grouping + type filter; default filter excludes nothing but bursts keep it readable |
| Two histories (audit vs activity) diverge | Audit stays admin-only; activity is the product timeline; both written from the same code paths |
| Deleted users/teams in old items | `actor_name` denormalised at write time; renderers tolerate missing IDs |
| Should system noise (bot sessions) be hidden by default? | Show, but chatbot sessions collapse by default; revisit after user feedback |

## 9. Acceptance criteria
- [ ] `/contacts/:id` shows header, editable fields, timeline and conversation history for any contact the user can access.
- [ ] The timeline merges messages (as bursts), calls, notes, tag changes, assignments, transfers, conversation status changes, chatbot sessions, campaign sends and field/lifecycle changes, newest first, with correct ordering across sources.
- [ ] Type filters and date range work; infinite scroll loads older items without duplicates or gaps.
- [ ] Items the user lacks permission for are not returned by the API.
- [ ] New activity appears in real time for open profiles.
- [ ] Clicking a message burst opens the chat at that conversation; clicking a campaign send opens the campaign.
- [ ] First timeline page p95 < 300 ms on a contact with 50k messages.


---

## Integration addendum (from [10 — System integration](10-system-integration.md))

- **Profile layout:**
  - The profile columns are the **ContactSidebar** sections (S12), the same component as the chat side panel.
  - The profile subscribes to `contact:<id>` through the new WS subscription model (S10) instead of `set_contact`, because `BroadcastToContact` also reaches idle clients.
- **Note composer:** it must not reuse the singleton `stores/notes.ts`. That store is per contact, cleared when chat unmounts, and receives other contacts' note events. Use a contact-keyed notes cache.
- **Shared renderers:** the **same activity renderers** appear in the chat thread as system pills (10 §4.1), so chat and profile tell the same story.
- **Corrections:**
  - `call_logs.contact_id` is a non-null UUID, not nullable.
  - Actor types include `bot` and `api`.
  - Chatbot session `timeout` is never written today; it appears only after the session-timeout job exists (S7/X14).
- **Deep links:** `message_burst` links need `GET /contacts/{id}/messages?around=<message_id|timestamp>`; the chat ignores query params today.
- **Access:**
  - Access uses `contactquery.Scope` = contact plus conversation visibility (S9).
  - The router guard for `/contacts/:id` must allow scoped agents; it currently requires `contacts:read`.
  - Every item serializer applies masking.
- **Entry points** now have owners:
  - the chat header name becomes an `EntityLink`
  - `ContactChip` in call logs, transfers, campaign recipients, tasks, deals and audit logs
  - notification links
  - the command palette (S12)
