# 08 — Automation on CRM Events

| | |
|---|---|
| **Priority** | **P2** |
| **Phase** | 3 |
| **Effort** | 4 engineer-weeks |
| **Depends on** | F2 event bus + stream, F4 scheduler, F5 notifications, F6 query engine, F10 messaging service, F11 webhook hardening, 01 fields, 03 conversations, 04 tasks, 05 param mappings / segments. 07 adds deal triggers/actions when shipped |
| **Unlocks** | — (multiplies the value of all other plans) |
| **Status** | Proposed |

## 1. Problem
Automation today is **conversation-level only**:
- **Keyword rules** match incoming text (`chatbot_processor.go:426`).
- **Chatbot flow graphs** run per session (`chatbot_graph_runner.go:65`).
- **Custom actions** run only when an agent clicks them (`custom_actions.go:270`).
- **`notification_rules`** exists in the schema but nothing uses it (`bulk.go:74`).

Nothing reacts to CRM changes: a tag being added, a lifecycle stage changing, a task becoming overdue, or a customer not replying for days.

## 2. Goals
Rules of the form **When** (trigger) → **Only if** (conditions) → **Then** (actions):
1. **Event triggers:** tag added/removed, field or lifecycle stage changed, contact created/assigned, conversation created/status changed/assigned, task created/completed/overdue, deal stage changed/won/lost (07).
2. **Time triggers:** "no customer reply in X after our last message", "no agent reply in X", "N days before/after a date field".
3. **Conditions:** any contact filter (F6 FilterBuilder: fields, tags, segment membership, conversation status, open tasks, …).
4. **Actions:** send template, send message (within 24 h window), assign conversation to team/user, set conversation status, add/remove tags, set field / lifecycle stage, set contact owner, create task, add note, call webhook, notify users, and deal actions (07).
5. **Reliability:** reliable, idempotent execution with run history, loop protection, rate limits and a dry-run tester.

### Non-goals (v1)
- Multi-step flows with waits/delays or branching. v1 is linear action lists; "wait" needs durable timers (future `automation_timers`).
- Triggering on `message.incoming`. Keyword rules and chatbot flows own live message handling, so there are no competing bots.
- A visual canvas builder. A form builder is faster to use for linear rules; the FlowCanvas could host v2 branching.

## 3. Reusable building blocks (from research)
| Block | Where | Use in automation |
|---|---|---|
| `evaluateConditionExpression` (expr-lang, pure) | `chatbot_graph_runner.go:502` | Optional advanced condition over `{event, contact}` |
| `processTemplate` (`{{var}}`, if/for) | `template_engine.go:30` | Templated text, titles, note bodies, webhook bodies |
| `executeConfiguredAPI` + SSRF-safe `HTTPClient` | `chatbot_processor.go:744`, `main.go:194` | `call_webhook` action |
| Webhook HMAC signing | `webhook_dispatch.go:201-221` | Optional signature on `call_webhook` |
| `createTransferToTeam` / assigner strategies | `agent_transfers.go:1318`, `assignment/assigner.go` | `assign_conversation` to team |
| `messaging.Service` (F10) | new | `send_template`, `send_message` |
| `conversation.Service` (03), `tasks` service (04), `customfields.SetValues` (01), `notify.Send` (F5) | new | respective actions |
| Custom actions JavaScript (goja, **no timeout**) | `custom_actions.go:495` | **Not reused** (unsafe) |

## 4. Design

### 4.1 Data model
```sql
automation_rules (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  name varchar(150) not null,
  description varchar(500) not null default '',
  enabled boolean not null default false,
  trigger_type varchar(64) not null,       -- catalog key, e.g. "contact.tag_added", "time.no_customer_reply"
  trigger_config jsonb not null default '{}',
  contact_filter jsonb null,               -- F6 AST, evaluated at run time
  condition_expression text not null default '', -- optional expr-lang over {event, contact, conversation}
  actions jsonb not null,                  -- ordered [{ "id": "a1", "type": "create_task", "config": {...}, "continue_on_error": false }]
  run_policy jsonb not null default '{"once_per_contact": false, "cooldown_minutes": 0, "max_runs_per_hour": 500}',
  created_by_id uuid not null, updated_by_id uuid null,
  last_run_at timestamptz null,
  run_count bigint not null default 0,
  error_count bigint not null default 0,
  consecutive_failures integer not null default 0,
  created_at, updated_at, deleted_at
);
create index idx_automation_rules_trigger on automation_rules (organization_id, trigger_type) where enabled and deleted_at is null;

automation_runs (
  id uuid pk default gen_random_uuid(),
  organization_id uuid not null,
  rule_id uuid not null,
  event_id uuid not null,                  -- F2 event id or synthetic time-trigger id
  event_type varchar(64) not null,
  contact_id uuid null,
  status varchar(20) not null,             -- succeeded | partially_failed | failed | skipped
  skip_reason varchar(50) null,            -- conditions_not_met | cooldown | once_per_contact | rate_limited | loop_depth | disabled
  depth smallint not null default 0,
  action_results jsonb not null default '[]', -- [{id, type, status, error, output: {...}}]
  dry_run boolean not null default false,
  started_at timestamptz not null,
  finished_at timestamptz null
);
create unique index idx_automation_runs_idem on automation_runs (rule_id, event_id) where dry_run = false;
create index idx_automation_runs_rule on automation_runs (rule_id, started_at desc);
create index idx_automation_runs_contact on automation_runs (contact_id, started_at desc);

automation_contact_state (
  rule_id uuid not null,
  contact_id uuid not null,
  subject_key varchar(100) not null default '', -- e.g. conversation id + waiting marker for time triggers
  last_run_at timestamptz not null,
  run_count integer not null default 0,
  primary key (rule_id, contact_id, subject_key)
);
```
- `notification_rules` is deprecated: removed from `GetMigrationModels` in this plan, and the table is dropped by an F1 migration after confirming it is empty.
- Retention: `automation_runs` older than 30 days are deleted by an F4 job (`automation_runs_retention`, daily).

### 4.2 Trigger catalog
| `trigger_type` | Kind | `trigger_config` | Event source |
|---|---|---|---|
| `contact.created` | event | `{sources?: ["whatsapp_inbound","import",…]}` | F2 |
| `contact.tag_added` / `contact.tag_removed` | event | `{tags: ["VIP"]}` (any of) | F3 instrumentation |
| `contact.field_changed` | event | `{field: "company", to?: {operator, value}}` | 01 |
| `contact.lifecycle_stage_changed` | event | `{from?: [..], to?: [..]}` | 01 |
| `contact.assigned` | event | `{to_user_ids?}` | F3 |
| `conversation.created` | event | `{accounts?}` | 03 |
| `conversation.status_changed` | event | `{to: ["resolved"], reasons?}` | 03 |
| `conversation.assigned` | event | `{team_ids?, user_ids?}` | 03 |
| `task.created` / `task.completed` / `task.overdue` | event | `{type_keys?}` | 04 |
| `deal.created` / `deal.stage_changed` / `deal.won` / `deal.lost` | event | `{pipeline_id, to_stage_ids?}` | 07 |
| `time.no_customer_reply` | time | `{after: {amount, unit: hours\|days}, statuses: ["pending","open"]}` — our last message older than X and no customer message since | scheduler |
| `time.no_agent_reply` | time | `{after: {amount, unit}}` — `conversations.waiting_since` older than X, conversation not bot-handled | scheduler |
| `time.date_field` | time | `{field: "renewal_date", offset_days: -7, at_local_hour: 9}` | scheduler |

**Time trigger job `automation_time_triggers`** (F4, every 5 min, leader-locked):
1. For each org with enabled time rules, run a bounded query per rule (limit 1,000 per tick).
   - *no reply* rules query `conversations` using `last_agent_message_at`, `last_customer_message_at`, `waiting_since`.
   - *date field* rules query `custom_field_values.value_date` in the org timezone at `at_local_hour`.
2. Each hit produces a synthetic event with deterministic `event_id = uuidv5(rule_id + subject_key)`.
   - no-reply `subject_key` = `conversation_id + ":" + last_agent_message_at` (a new agent message re-arms the rule)
   - date `subject_key` = `contact_id + ":" + date value + offset`
3. `automation_contact_state` and the unique run index guarantee the rule fires once per subject.

### 4.3 Action catalog
Each action implements `Validate(config, orgCtx) error` and `Execute(ctx, runCtx) (output, error)` with typed errors `Permanent` or `Retryable`.

| `type` | Config | Implementation | Notes |
|---|---|---|---|
| `send_template` | `{account: "contact_last"\|"<name>", template_id, param_mappings}` (05 shape) | `messaging.PrepareTemplate` + `SendTemplate` | Marketing opt-out → **skipped** (permanent). Also creates/reopens the conversation as pending (03 rule for agent-initiated outreach, actor automation) |
| `send_message` | `{text}` (templated) | `messaging.SendText(RequireServiceWindow=true)` | Outside 24 h window → failed (permanent) with a clear reason |
| `assign_conversation` | `{mode: team\|user\|unassign, team_id?, user_id?}` | team → `createTransferToTeam` (strategy + business hours); user → 03 assign | Creates a conversation if none is active |
| `set_conversation_status` | `{status, snooze_for?: {amount, unit}}` | `conversation.Service` | |
| `add_tags` / `remove_tags` | `{tags: []}` | tags update path (emits F3 events with origin) | Unknown tags auto-created (colour gray) |
| `set_field` | `{field, value}` (value templated for text) | `customfields.SetValues` | Includes `lifecycle_stage` |
| `set_contact_owner` | `{mode: user\|team_round_robin\|conversation_assignee, …}` | contacts assign + assigner | |
| `create_task` | `{type_key, title, description?, owner: {mode: contact_owner\|conversation_assignee\|user\|team_round_robin, …}, due_in: {amount, unit}, all_day?, priority?}` | tasks service, `source='automation'`, `automation_rule_id` | Fallback owner: rule creator |
| `add_note` | `{content}` (templated) | notes create, author = automation actor | Displayed as "Automation: <rule name>" |
| `call_webhook` | `{url, method, headers, body, sign: bool, timeout_s ≤ 10}` | `executeConfiguredAPI` with SSRF-safe client; HMAC with an org-level automation secret | Logged in `webhook_deliveries` (F11); 5xx/timeout → retryable |
| `notify_users` | `{recipients: {user_ids?, roles?, contact_owner?, conversation_assignee?}, title, body}` | `notify.Send` (type `automation`) | |
| `create_deal` / `move_deal_stage` | `{pipeline_id, stage_id, title, value?}` / `{stage_id}` | deals service (07) | Available once 07 ships |

**Template variables** available in every templated string (via `processTemplate`):
- `contact.name`, `contact.phone_number`, `contact.fields.<key>`, `contact.tags`, `contact.owner.name`
- `conversation.status`, `conversation.assignee.name`
- `event.type` and `event.data.*` (e.g. `event.data.tag`, `event.data.to`)
- `task.*` / `deal.*` when the trigger subject is a task or deal
- `rule.name`, `now` (org timezone)

### 4.4 Execution engine (`internal/automation`)
```
F2 Bus ──StreamSink──► Redis stream wacrm:crm_events ──consumer group "automation"──► Engine.Handle(event)
Scheduler (time triggers) ─────────────── synthetic events ──────────────────────────►┘
```
- **Where it runs:** in the **server process**, one consumer per replica in the same consumer group (each event is processed once). The server holds the services the actions need (`conversation`, `transfers`, `messaging`, …), avoiding a large worker refactor. It is started next to the scheduler in `main.go`. If moved to the worker later, the services are already package-level (F10, 03, 04).
- **`Engine.Handle(event)`:**
  1. Skip if the org's automation kill-switch is off (`organizations.settings.automations.enabled`, default true).
  2. Load enabled rules for `(org, event.Type)` (Redis-cached, invalidated on rule write).
  3. For each rule:
     - **Trigger config match:** a pure function per trigger type.
     - **Loop protection:** skip if `event.Origin.Depth >= 3`, or if `event.Origin.AutomationRuleID == rule.ID` (no self-trigger).
     - **Run policy:**
       - `once_per_contact` / `cooldown_minutes` via `automation_contact_state`
       - `max_runs_per_hour` via a Redis counter `wacrm:auto:rate:<rule>:<hour>` → `skipped(rate_limited)`; also notify the rule creator once per hour
     - **Conditions:** compile `contact_filter` with F6, no viewer scope, and check `WHERE contacts.id = $contact`. Evaluate `condition_expression` via expr-lang, capped at 100 ms and memory-limited.
     - **Insert** `automation_runs` (unique `(rule_id, event_id)`); on conflict the run already happened, so skip (idempotent redelivery).
     - **Execute actions** sequentially with actor `{type: automation, id: rule.ID, name: rule.Name}`. Events emitted by actions carry `Origin{AutomationRuleID: rule.ID, Depth: event.Depth+1}`.
     - **Record** per-action results; update `run_count` / `error_count` / `consecutive_failures`.
  4. XACK the stream entry after all rules are recorded.
- **Retries:** retryable failures leave the run `failed` with `retry_at`. The consumer re-claims pending stream entries older than 1 min (XAUTOCLAIM) up to 3 attempts; a completed rule's run row prevents re-running succeeded rules. Retried runs re-execute **only failed actions** (action ids in `action_results`).
- **Auto-disable:** 10 consecutive failed runs → `enabled=false`, notify the creator and admins (F5), and record an audit entry.
- **Limits:** ≤ 10 actions per rule, ≤ 200 rules per org, `call_webhook` timeout ≤ 10 s, one engine goroutine pool per replica (default 8 workers).

### 4.5 API
| Method & path | Purpose | Permission |
|---|---|---|
| `GET /api/automations` | List with stats (runs 24 h, failures 24 h, last run) | `automations:read` |
| `POST /api/automations` / `GET/PUT/DELETE /api/automations/{id}` | CRUD (validation of trigger/actions/conditions; audit logged) | `automations:write/delete` |
| `POST /api/automations/{id}/enable` / `…/disable` | Toggle | `automations:write` |
| `POST /api/automations/{id}/test` `{contact_id, event_sample?}` | **Dry run**: evaluates trigger match (sample), conditions, and renders each action (resolved template text, chosen owner/team, template params) without side effects; records a `dry_run` run | `automations:write` |
| `GET /api/automations/{id}/runs?status=&contact_id=&cursor=` | Run history | `automations:read` |
| `GET /api/automations/catalog` | Triggers + actions + variables schema for the builder UI (only those enabled by shipped features) | `automations:read` |
| `GET /api/contacts/{id}/automation-runs` | Runs affecting a contact (profile debug panel) | `automations:read` + contact access |

Events emitted by automation are recorded as activity with `actor_type='automation'`, so the contact timeline (02) shows "Automation *Overdue follow-up* created task …".

### 4.6 Frontend
- **Navigation:** CRM section → **Automations** (`/automations`, icon `Zap`, `permission: 'automations'`); update `navigationOrder`.
- **List** (`views/automations/AutomationsView.vue`): name, "When …" summary sentence, enabled switch, last run (relative), runs/failures (24 h) with a red dot on failures, actions menu (duplicate, delete). "New automation" opens a **recipe picker**:
  - Blank
  - "Tag added → assign to team"
  - "Task overdue → notify manager"
  - "No reply in 3 days → send follow-up template + create check-in task"
  - "Lifecycle becomes Customer → send welcome template"
  - "Conversation resolved → add tag *Served*"
- **Builder** (`views/automations/AutomationDetailView.vue`, `DetailPageLayout`, unsaved-changes guard):
  1. **When:** trigger select (grouped: Contacts, Conversations, Tasks, Deals, Time) + trigger-specific form (tag picker, field + operator/value using `FieldInput`, stage pickers, duration inputs, date field picker + offset + local hour).
  2. **Only if:** `FilterBuilder` (F6) for contact conditions + collapsible "Advanced expression" editor (monospace textarea with variable hints and validation via `/test`).
  3. **Then:** ordered action cards (vuedraggable reorder, add menu grouped by category, remove, "continue on error" toggle). Each card has a type-specific form:
     - template picker + `ParamMappingEditor` (shared with 05)
     - text with a variable inserter
     - team/user pickers
     - task form subset (04)
     - webhook URL/method/headers/body with a sign toggle
  4. **Settings:** once per contact, cooldown, max runs per hour.
  5. **Header actions:** Enable/Disable, Save, **Test** (contact picker → shows the dry-run result timeline: trigger ✓, conditions ✓/✗ with the failing rule highlighted, each action preview).
  6. **Runs** tab: DataTable of runs with status chips and skip reasons; expanding a row shows per-action results and errors; link to the contact.
- **Contact profile (02):** "Automation runs" collapsible panel (for `automations:read`).
- **Notifications (F5):** `automation` type for notify actions, auto-disable and rate-limit alerts.

## 5. Migration & rollout
1. Schema: rules, runs, contact_state; remove `NotificationRule` from AutoMigrate; F1 migration drops `notification_rules` if empty (else leaves it and logs).
2. F1 permissions `automations` (admin all; manager read/write).
3. Enable the F2 StreamSink only for orgs with enabled rules (cached flag).
4. **Rollout behind org setting `automations.enabled`** (default true) + global config `automation.enabled` (default true) to switch off the engine per deployment if needed.
5. Dogfood with recipes on the demo org.

## 6. Milestones
| # | Deliverable | Est. |
|---|---|---|
| M1 | Models, catalog types, validation, CRUD API + audit | 3 d |
| M2 | Engine: stream consumer group, matching, loop/rate/once/cooldown policies, idempotent runs, retries, auto-disable | 4 d |
| M3 | Actions: tags, fields, owner, conversation assign/status, notes, notify | 3 d |
| M4 | Actions: send_template/send_message (F10), create_task (04), call_webhook (F11 log) | 3 d |
| M5 | Time triggers job (no customer reply, no agent reply, date field) | 2 d |
| M6 | Dry-run tester + catalog endpoint | 1.5 d |
| M7 | UI: list, recipes, builder, runs tab, profile panel | 4 d |

## 7. Testing
- **Engine unit tests:**
  - trigger config matching per type
  - self-trigger and depth-3 loop stop (rule A adds tag X, rule B on tag X adds tag Y, rule C on Y adds X → stops at depth 3)
  - once-per-contact and cooldown
  - rate limit
- **Idempotency:** deliver the same stream entry twice → one run; kill after action 1 → retry runs only failed/unexecuted actions.
- **Actions:**
  - send_template to an opted-out contact with a MARKETING template → skipped with reason
  - send_message outside the window → failed permanent
  - assign_conversation respects business hours (out-of-hours message sent, no transfer)
  - call_webhook to a private IP blocked by the SSRF-safe client
- **Time triggers:** no-reply fires once per waiting period and re-arms after a new agent message; date field fires at the local hour in the org timezone once.
- **Dry run:** no rows changed in contacts/tasks/messages; a run recorded with `dry_run=true`.
- **E2E** (`e2e/tests/automations/*`):
  - create a recipe "Tag VIP → create task" → tag a contact in chat → task appears in My tasks
  - the Runs tab shows success
  - disable the rule → no new task

## 8. Risks & open questions
| Risk / question | Mitigation / proposal |
|---|---|
| Runaway automations spamming customers | Loop depth limit, self-trigger block, per-rule hourly cap, once/cooldown policies, opt-out respect, window enforcement, auto-disable on failures, org kill switch |
| Overlap with keyword rules/chatbot | No `message.incoming` trigger in v1; document "use Chatbot for replies, Automations for CRM reactions" |
| Engine in server process ties throughput to API replicas | Consumer pool size configurable; move to worker later (services already extracted) |
| Complex condition expressions misused | Optional, time/memory limited, validated in dry run |
| Should managers create send-template automations? | Yes by default (manager has `automations:write`); orgs can remove it via roles |

## 9. Acceptance criteria
- [ ] Admins/managers can create rules with any catalog trigger, contact conditions and up to 10 actions, enable/disable them, and test against a real contact without side effects.
- [ ] Event-triggered rules run within seconds of the triggering change, exactly once per event, even with multiple server replicas and redelivery.
- [ ] Time triggers "no customer reply in X", "no agent reply in X" and "N days before/after a date field" fire once per subject in the org timezone.
- [ ] Actions send templates (respecting opt-out), send in-window messages, assign conversations to teams using the configured strategy, set status/tags/fields/owner, create tasks, add notes, call signed webhooks and notify users.
- [ ] Automation loops stop at depth 3; rate limits and auto-disable work and notify the rule owner.
- [ ] Each run is visible with per-action results; contact timelines attribute changes to the automation.


---

## Integration addendum (from [10 — System integration](10-system-integration.md))

**Engine:**
- **Action library:** actions come from the shared **`internal/crmactions`** library (S7). The same executors, config schema and Vue action cards are used by chatbot CRM nodes, keyword rules, slash commands and bulk actions. This plan keeps triggers, run policies, runs, retries and the builder.
- **Input:** the engine consumes the **outbox-relayed** stream (S3). Its own action events are written to the outbox with origin and depth.
- **Transfers:** `assign_conversation` uses `transfers.Service` typed outcomes (`queued`, `suppressed_out_of_hours`, `already_active`), so runs record real results instead of calling the fire-and-forget `createTransferToTeam`.
- **Condition expressions:** they run through a timeout wrapper, because `expr.Run` has no context.

**Interplay with the chatbot:**
- `send_template`, `send_message` and `assign_conversation` default to **"skip while a bot session or flow is active"**, because `contact.created`, `conversation.created` and reopen fire on the same inbound message the chatbot is handling.
- Automation sends use `sender_type=automation` and do not arm chatbot client-inactivity reminders.
- The builder warns when `time.no_customer_reply` overlaps chatbot client-inactivity reminders, or `time.no_agent_reply` overlaps the SLA warning.

**Actions and references:**
- **`add_note`** requires `conversation_notes` to allow a nullable author with `author_type=automation`.
- **New triggers:** `call.missed`, `call.completed`, `chatbot.flow_completed`, `campaign.replied`, `conversation.sla_breached`.
- **`send_template.account`** stores `whatsapp_account_id` (S8).
- **Broken references:** users, teams, templates, tags and field options referenced in configs are tracked by `entityrefs`. A broken reference marks the action broken and notifies the owner instead of failing silently.
- **`call_webhook`** uses the SSRF-safe client shared with the IVR `http_callback`, and logs to `webhook_deliveries`.
- **Graph-compatible format:** the action list maps 1:1 to a linear `flowgraph.Graph`, so v2 branching can reuse `internal/flowgraph`.
