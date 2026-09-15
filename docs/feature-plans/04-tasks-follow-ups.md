# 04 — Tasks and Follow-ups

| | |
|---|---|
| **Priority** | **P1** — the single biggest step from shared inbox to CRM |
| **Phase** | 2 |
| **Effort** | 2.5 engineer-weeks |
| **Depends on** | 02 (profile panel + timeline), 03 (link tasks to conversations), F2 events, F3 activity, F4 scheduler, F5 notifications, F6 access scope, F9 timezone |
| **Unlocks** | 06 merge (re-point tasks), 07 deals (tasks on deals), 08 automation (`create_task` action, `task.overdue` trigger), 09 overdue-per-agent reports |
| **Status** | Proposed |

## 1. Problem
There is no task model. The only "reminders" are the chatbot's client-inactivity messages to customers (`sla_processor.go:466`). Agents cannot record "call Mia back Thursday" or "send the quote", nobody is reminded, and managers cannot see overdue follow-ups.

## 2. Goals
1. **Task record:** a **task** has a contact, an owner, a due date/time, a status and a type (call back, send quote, check in, …).
2. **My tasks:** a **My tasks** view with Overdue / Due today / Upcoming / Completed, plus a team view for managers.
3. **Creating tasks** from the chat panel, the contact profile, a message ("follow up on this"), and via API/automation.
4. **Notifications** when a task is due (at a reminder time) and when it becomes overdue; in-app via F5.
5. **Events and visibility:** task events on the contact timeline, webhooks and automation triggers.

### Non-goals (v1)
- Recurring tasks, sub-tasks, checklists, task comments.
- Email/push reminders (F5 has in-app only).
- Calendar sync.

## 3. Current state
- No `tasks` table. `notification_rules` exists but is unused (`bulk.go:74`); it is not repurposed here (08 replaces it).
- No notification centre (F5 adds it).
- Agents' availability (`users.is_available`) exists and is used for assignment (`assigner.go:150`).
- The chat side panel (`ContactInfoPanel.vue`) has Tags → Metadata → Session data sections; 01 adds Details.

## 4. Design

### 4.1 Data model
```sql
task_types (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  key varchar(50) not null,             -- call_back, send_quote, check_in, follow_up, other
  label varchar(100) not null,
  icon varchar(50) not null default 'check-square', -- lucide name
  color varchar(20) not null default 'gray',
  default_due_offset_minutes integer not null default 1440,
  is_system boolean not null default false,
  position integer not null default 0,
  archived_at timestamptz null,
  created_at, updated_at, deleted_at
);
create unique index idx_task_types_org_key on task_types (organization_id, key) where deleted_at is null;

tasks (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  contact_id uuid not null,
  conversation_id uuid null,            -- 03, optional context
  deal_id uuid null,                    -- 07, optional
  message_id uuid null,                 -- created from a specific message
  type_id uuid not null,
  title varchar(255) not null,
  description text not null default '',
  owner_id uuid not null,               -- responsible user
  created_by_id uuid null,              -- null for automation/system
  priority varchar(10) not null default 'normal', -- low | normal | high
  status varchar(20) not null default 'open',     -- open | completed | cancelled
  due_at timestamptz not null,
  all_day boolean not null default false,         -- due at end of day in owner's timezone
  remind_at timestamptz null,           -- explicit reminder time (default: due_at - org default lead)
  completed_at timestamptz null, completed_by_id uuid null,
  cancelled_at timestamptz null,
  source varchar(20) not null default 'manual',   -- manual | automation | api | chatbot
  automation_rule_id uuid null,         -- 08
  reminder_sent_at timestamptz null,
  overdue_notified_at timestamptz null,
  created_at, updated_at, deleted_at
);
create index idx_tasks_owner_open   on tasks (organization_id, owner_id, due_at) where status = 'open' and deleted_at is null;
create index idx_tasks_contact      on tasks (contact_id, status, due_at);
create index idx_tasks_reminder_due on tasks (remind_at) where status = 'open' and reminder_sent_at is null;
create index idx_tasks_overdue_scan on tasks (due_at)    where status = 'open' and overdue_notified_at is null;
create index idx_tasks_deal         on tasks (deal_id) where deal_id is not null;
```
- **Overdue is derived** (`status = 'open' and due_at < now()`), not stored. `overdue_notified_at` only records that notification happened.
- **All-day tasks:** `due_at` is stored as 23:59:59 in the owner's timezone (F9), converted to UTC.
- **Default task types** (F1 migration seeds for all orgs and on org create):
  - `call_back` "Call back" (phone), `send_quote` "Send quote" (file-text), `check_in` "Check in" (message-circle), `follow_up` "Follow up" (repeat), `other` "Other" (check-square)
  - system types can be relabelled or archived but not deleted

### 4.2 Backend
**Package** `internal/tasks` (service) + `internal/handlers/tasks.go` (HTTP), following `canned_responses.go` conventions.

**Service rules**
- **Contact:** must exist in the org and be accessible to the actor (`contactquery.Scope`, F6). The owner must be an active member of the org.
- **Owner choice:**
  - defaults to the actor
  - a manager-level user (`tasks:write` + `contacts:read`) may assign to anyone
  - agents may assign to themselves, or to others only if they have `tasks:write` **and** the org setting `tasks.agents_can_assign_others` (default true)
- **`remind_at`:** defaults to `due_at - organizations.settings.tasks.default_reminder_minutes` (default 15; 0 = at due time). For all-day tasks it defaults to 09:00 in the owner's timezone on the due date.
- **Complete / reopen / cancel** transitions record `completed_*` / `cancelled_at`.
- **Owner reassignment** resets `reminder_sent_at` / `overdue_notified_at` if still in the future / not yet due.
- **Conversation link:** creating a task from the chat panel links `conversation_id` to the open conversation (03).

**Routes**
| Method & path | Purpose | Permission |
|---|---|---|
| `GET /api/tasks?view=mine\|team\|all&owner_id=&status=open\|completed\|cancelled&due=overdue\|today\|upcoming\|no_date&type=&contact_id=&deal_id=&search=&page=&limit=&sort=due_at` | List (server-side `today` boundaries in the viewer's timezone) | `tasks:read` (scoped) |
| `GET /api/tasks/counts` | `{overdue, today, upcoming}` for the current user (sidebar badge) | `tasks:read` |
| `POST /api/tasks` | Create | `tasks:write` |
| `GET /api/tasks/{id}` / `PUT /api/tasks/{id}` / `DELETE /api/tasks/{id}` | CRUD (delete = soft) | `tasks:read/write/delete` |
| `POST /api/tasks/{id}/complete` / `…/reopen` / `…/cancel` | Transitions | `tasks:write` |
| `POST /api/tasks/bulk` `{ids[], action: complete\|reassign\|reschedule\|delete, payload}` | Up to 200 | `tasks:write` (+`delete`) |
| `GET /api/contacts/{id}/tasks` | Panel list | contact access + `tasks:read` |
| `GET/POST/PUT /api/task-types`, `PUT /api/task-types/reorder` | Types admin | `tasks:read` / settings via `tasks:delete` (admin) |

**Visibility (`view`):**
- `mine` = `owner_id = me`.
- `team` = owners in teams where the viewer is a team manager (`team_members.role='manager'`).
- `all` requires `contacts:read` and `tasks:delete` (manager/admin default).
- Agents always see tasks they own **and** tasks on contacts they can access (read-only unless they own them or have manager rights).

**Events** (F2): `task.created`, `task.updated` (owner/due changes; no webhook), `task.completed`, `task.cancelled`, `task.due`, `task.overdue`.
- Activity records created / completed / cancelled / overdue on the contact timeline (02 renderer `TimelineTask.vue`).
- Webhooks for created/completed/overdue.
- Automation triggers for created/completed/overdue/due.

**Scheduler job `task_due_notifier`** (F4, every minute, leader-locked)
1. **Reminders:** `status='open' and reminder_sent_at is null and remind_at <= now()`, batch 500.
   - `notify.Send(type='task_due', user=owner, title="Due at 15:00: Call back Mia", link="/tasks?focus=<id>")`
   - publish `task.due`
   - set `reminder_sent_at`
2. **Overdue:** `status='open' and overdue_notified_at is null and due_at <= now()`.
   - `notify.Send(type='task_overdue', …)` to the owner
   - if `organizations.settings.tasks.notify_manager_after_minutes` > 0 and the overdue duration passes that threshold, also notify team managers of the owner's teams (second pass guarded by `data.manager_notified`)
   - publish `task.overdue`
   - set `overdue_notified_at`
3. Both passes are idempotent (row update with `WHERE … is null` returns rows affected; notify only on success).

**WS:** `task_updated` to the owner and to viewers of the contact (sidebar counts + panels refresh).

### 4.3 Frontend
**Navigation:** Main section → **Tasks** (`/tasks`, icon `CheckSquare`, `permission: 'tasks'`), with a count badge = overdue + due today (from `/tasks/counts`, updated by `task_updated` / `notification_created`). `SidebarNavItem` gains an optional `badge` prop. Update `navigationOrder`.

**My tasks page** (`views/tasks/TasksView.vue`)
- **Header:** "Tasks" with view switcher (Mine / Team / All by permission) and "New task" button.
- **Tabs:** Overdue (red count) · Today · Upcoming · Completed. Filters: type, owner (team/all), priority, contact search, date range for Completed.
- **List grouped by day:**
  - checkbox to complete, with optimistic update and a toast with Undo (5 s)
  - type icon, title, contact chip (→ profile), due time relative with absolute time on hover
  - owner avatar (team view), priority flag
  - inline reschedule menu: +1 hour, Tomorrow 9:00, Next week, Pick date
- **Keyboard:** `n` new, `x` complete focused, `r` reschedule.
- **Row click:** opens `TaskDetailSheet.vue` (reka `Sheet`) with editable fields, link to conversation/message, activity (task-level audit), delete.
- **Empty states:** "No overdue tasks — nice work" (no gradient hero; follows the app's `EmptyState`).
- **Bulk bar:** complete, reassign, reschedule.

**Create/edit** (`components/tasks/TaskFormDialog.vue`)
- **Fields:**
  - type (select; sets the default due offset)
  - title (prefilled "Call back {contact name}" per type)
  - contact (fixed when opened from a contact)
  - owner (default me)
  - due date + time or all-day
  - reminder (At due time / 15 min before / 1 hour before / Morning of / None)
  - priority, description
- Uses the F9 user timezone. Date/time input: existing `Calendar` + time select components (no new date library; `Intl` formatting).

**Contact integrations**
- **Chat side panel:** new **Tasks** section (after Details) showing open tasks for the contact, sorted by due, overdue in red, with a quick "+ Follow-up" button that opens the dialog with presets.
- **Message hover action** in chat: "Create follow-up" pre-fills the title with a message excerpt and sets `message_id`.
- **Contact profile (02):** Tasks panel in the right column + `TimelineTask.vue` renderer.
- **Conversation header (03):** when resolving a conversation, an optional "Resolve & add follow-up" split-menu item.

**Notifications (F5):** `task_due`, `task_overdue`, `task_assigned` (when someone else assigns you) with links to `/tasks?focus=<id>`.

**Settings:**
- Settings → **Task Types** (`/settings/task-types`): list with drag reorder, label/icon/colour/default due offset, archive.
- Settings → General → Tasks: default reminder minutes, agents can assign others, manager escalation delay.

## 5. Migration & rollout
1. Schema via AutoMigrate + indexes + testutil list.
2. F1 migrations: seed task types for all orgs; permissions `tasks` (admin/manager all; agent read/write).
3. Register the scheduler job; add the notification types to F5 preferences.
4. Ship backend, then Tasks page + panel + nav badge.

## 6. Milestones
| # | Deliverable | Est. |
|---|---|---|
| M1 | Models, indexes, seeds, service with scope/owner rules + unit tests | 2 d |
| M2 | HTTP routes (list views with timezone boundaries, CRUD, transitions, bulk, counts) + events + audit | 2 d |
| M3 | Scheduler notifier (reminder, overdue, manager escalation) + tests with fake clock | 1.5 d |
| M4 | Tasks page, detail sheet, form dialog, nav badge, WS | 3 d |
| M5 | Chat panel section, message action, profile panel + timeline renderer, settings pages | 2 d |

## 7. Testing
- **Timezone:** "today" for a user in `Asia/Karachi` at 23:30 local vs UTC boundary; all-day due handling across DST (`America/New_York`).
- **Scope:** an agent cannot create a task on an inaccessible contact (404); an agent without assign-others cannot set a different owner (403).
- **Scheduler:** with a clock injected, a reminder is sent exactly once; overdue fires once; manager escalation after the threshold; two schedulers → one notification (F4 lock).
- **Events:** complete → activity row + webhook payload + automation stream entry (when the org has rules).
- **E2E** (`e2e/tests/tasks/*`, page object `TasksPage`):
  - create from the chat panel → appears in My tasks Today
  - complete with undo
  - an overdue task shows a badge (API helper sets `due_at` in the past + triggers the job in test mode)

## 8. Risks & open questions
| Risk / question | Mitigation / proposal |
|---|---|
| Notification fatigue for managers | Manager escalation off by default (`notify_manager_after_minutes = 0`) |
| Tasks without a contact (internal to-dos)? | Out of scope; `contact_id` is required to keep tasks CRM-centric. Revisit if requested |
| Owner deactivated/removed from org | Nightly (F4) job reassigns open tasks to the user's team manager or leaves them in an "Unowned" filter visible to admins; open question for product — proposal: surface in the All view with an "Owner inactive" badge, no auto-reassign |
| Should completing the last open task resolve the conversation? | No; conversation status is independent (03). Automation (08) can do it |

## 9. Acceptance criteria
- [ ] Users can create, edit, reschedule, complete (with undo), reopen and cancel tasks from the Tasks page, chat panel, contact profile and a chat message.
- [ ] My tasks shows correct Overdue / Today / Upcoming / Completed buckets in the user's timezone; the sidebar badge shows overdue + today.
- [ ] Reminder and overdue notifications arrive once per task, in the bell and as toasts, with working deep links.
- [ ] Managers can see team tasks; agents see their own and those on accessible contacts only.
- [ ] Task created/completed/overdue appear on the contact timeline and are delivered as webhooks.
- [ ] Admins can customise task types; defaults exist for every org after migration.


---

## Integration addendum (from [10 — System integration](10-system-integration.md))

- **UI building blocks:**
  - Task detail uses the shared **RecordSheet** (S12), also used by deals. `components/ui/sheet` has no existing usage, so this plan and 07 establish the pattern together.
  - Lists use DataTable v2 and `useListViewState` (Mine / Team / All + saved filters).
- **Creation entry points** share one `TaskFormDialog`:
  - composer slash command `/task`
  - message hover/long-press "Create follow-up"
  - conversation "Resolve & add follow-up"
  - **call outcome dialog "Schedule callback"** (adds `tasks.call_log_id`)
  - the chatbot CRM action node and automation `create_task` (both from the S7 action library)
- **Home and badges:**
  - Agents land on **Home** (`/home`), which shows My tasks due/overdue. Agents cannot open the Dashboard because it requires `analytics`.
  - The sidebar badge uses the nav `badgeKey` mechanism (S12). The Inbox unread badge ships in the same change so the sidebar stays consistent.
- **User lifecycle** (S8): deactivated or removed owners keep their tasks, with an "Owner inactive" flag and a bulk-reassign prompt. This resolves open question §8. Automation-created tasks keep a real owner; the rule-creator fallback moves with automation ownership when a user is removed.
- **Time and masking:** due buckets, reminders and all-day handling use `internal/schedule` + `useFormatters` (S11). Serializers apply phone masking.
- **Shortcuts:** `n`, `x` and `r` register in the global `useShortcuts` registry with input-focus guards.
