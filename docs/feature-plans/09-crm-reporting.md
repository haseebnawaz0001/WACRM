# 09 — CRM Reporting

| | |
|---|---|
| **Priority** | **P3** — with a **quick win** (agent response/resolution times) shipped right after 03 |
| **Phase** | 4 (quick win at end of Phase 1) |
| **Effort** | 2.5 engineer-weeks (quick win: 2 days, included in 03 M7) |
| **Depends on** | 01 (source, lifecycle stage), 03 (conversation timings), 04 (tasks), 07 (deal stage history), F3 activity (lifecycle changes), F9 timezone |
| **Unlocks** | — |
| **Status** | Proposed |

## 1. Problem
Reporting today covers messages, contacts, campaigns, transfers and chatbot sessions through dashboard widgets (`handlers/widgets.go`), plus agent analytics based on transfers (`agent_analytics.go`). CRM questions cannot be answered:
- Where do new contacts come from?
- How many leads become customers?
- Which agents have overdue follow-ups?
- How fast do agents respond and resolve?
- What is in the pipeline and what converts?

Existing agent analytics also has gaps:
- `AvgFirstResponseMins` / `AvgResponseMins` are never populated.
- Queue time uses `updated_at - transferred_at`.

## 2. Goals
1. **New contacts by source** over time, optionally split by lifecycle stage.
2. **Conversion between stages:**
   - lifecycle funnel (contacts)
   - pipeline funnel, win rate and time-in-stage (deals, 07)
3. **Overdue tasks per agent**, plus completion and on-time rates.
4. **Response and resolution times per agent:** median, p90, volume, SLA breach share.
5. **Dashboard integration:** the existing dashboard widgets can host these (new data sources + display types), and an **Analytics → CRM Reports** page groups them with filters and CSV export.

### Non-goals (v1)
- Custom report builder beyond widget configuration.
- Scheduled email reports (no SMTP).
- Data warehouse export.

## 3. Current state (widgets & analytics)
| Area | Fact | Ref |
|---|---|---|
| Widget sources | messages, contacts, campaigns, transfers, sessions; metrics count/sum/avg; display number/percentage/chart/table/shortcuts; charts line/bar/pie | `widgets.go:121-133` |
| Query building | per-source `queryX`, `getChartData` (day buckets), `getGroupedData`, `getGroupedTimeSeriesData`, raw SQL with whitelisted columns | `widgets.go:745-1353` |
| Inconsistencies | contacts `is_read` filter dropped; transfers `source` group-by missing for bar/pie; time-series skips group-by whitelist | `widgets.go:990-1160` |
| Dates | `parseDateRange` YYYY-MM-DD, UTC, current month default | `helpers.go:121` |
| Agent analytics | transfers handled, queue time (inaccurate), resolution (`resumed_at - transferred_at`), break time, trend; first response fields empty | `agent_analytics.go` |
| Frontend | `DashboardView.vue` grid-layout-plus + vue-chartjs; `AgentAnalyticsView.vue` | frontend research |

## 4. Design

### 4.1 Query layer refactor (`internal/reports`)
Move widget SQL building into a registry-based package used by both widgets and CRM reports:
```go
type DataSource struct {
    Key         string              // "conversations"
    Table       string              // base table + joins
    DateColumn  string              // default time column
    Dimensions  map[string]Dim      // group-by: column/expression + label resolver (user names, option labels)
    Measures    map[string]Measure  // count, sum(value), avg(duration), median(duration), p90(duration)
    Filters     map[string]FilterField
    Scope       func(q Query, viewer Viewer) // agent sees own rows where applicable
}
```
- **Fixes the inconsistencies above:** one whitelist per source drives filters, group-by and time series.
- **Time bucketing in the org timezone** (F9): `date_trunc($interval, ts AT TIME ZONE $tz)`.
- **Percentiles** via `percentile_cont(0.5|0.9) WITHIN GROUP (ORDER BY …)`.
- **Cache:** results cached in Redis for 5 minutes, keyed by `hash(org, source, params, viewer-scope)`, bypassable with `?fresh=1` for admins.
- **Safety:** existing widget API responses stay byte-compatible; existing widget tests must pass.

**New data sources**
| Source | Base | Dimensions | Measures |
|---|---|---|---|
| `contacts` (extended) | contacts ⟕ custom_field_values (source, lifecycle_stage) | `field.source`, `field.lifecycle_stage`, `whatsapp_account`, `assigned_user_id` | count |
| `lifecycle_changes` | contact_activities where type=`contact.lifecycle_stage_changed` | `data.from`, `data.to`, `actor_type` | count distinct contact |
| `conversations` (03) | conversations | `status`, `assignee_id`, `first_responder_id`, `resolved_by_id`, `team_id`, `resolution_reason`, `whatsapp_account` | count, median/p90/avg `first_response` (`first_response_at - first_customer_message_at`), median/p90/avg `resolution` (`resolved_at - opened_at`) |
| `tasks` (04) | tasks | `owner_id`, `type`, `status`, `priority`, `is_overdue` (expr) | count, overdue count, on-time completion rate |
| `deals` (07) | deals | `pipeline_id`, `stage_id`, `owner_id`, `status`, `lost_reason` | count, sum(value), weighted sum |
| `deal_stage_moves` (07) | deal_stage_history | `from_stage_id`, `to_stage_id`, `moved_by_id` | count, median `time_in_from_stage` |

**New widget display types:** `funnel` (ordered stages with conversion %), `leaderboard` (table grouped by user with several measures, sortable). Existing types are unchanged.

### 4.2 Reports (definitions)
**R1 — New contacts by source**
- New contacts per interval (day/week/month) stacked by `field.source`. Contacts with no source are shown as "Unknown".
- Toggle: split by current lifecycle stage.
- Totals table: source, contacts, share %, and "became Customer" count within the period.

**R2 — Lifecycle funnel**
- Stages ordered by the `lifecycle_stage` option order (01).
- A contact counts at a stage if it **reached** that stage (or a later one) during the period. "Reached" = a `contact.lifecycle_stage_changed` event with `to` = that stage, or creation with that default.
- Output: count per stage and step conversion (stage n+1 / stage n), plus median days between consecutive stages (from event timestamps per contact).
- Caveat shown in UI: backwards moves count once at the highest stage reached.

**R3 — Pipeline funnel & forecast (07)**
- **Funnel:** deals that entered each open stage in the period (`deal_stage_moves`), step conversion, overall win rate (won / (won + lost) closed in period), median time in stage.
- **Forecast:** open deals by `expected_close_date` month; sum and probability-weighted sum; overdue close dates flagged.
- **Lost reasons** breakdown.

**R4 — Tasks by agent (04)**
- Leaderboard per owner: open, overdue now, due today, completed in period, completed on time %, median completion delay (completed_at - due_at for late ones).
- Filter by team and type. Clicking a count opens `/tasks?owner_id=…&due=overdue`.

**R5 — Agent performance (03)** — the quick win
- Leaderboard per agent, for conversations in the period:
  - handled (first responder or resolver)
  - median / p90 first response
  - median / p90 resolution (agent-resolved only)
  - reopened rate (`reopened_count > 0`)
  - SLA breach % (transfers with `sla_breached`)
  - messages sent
  - break time (existing `UserAvailabilityLog` logic)
- Business-hours-aware durations are out of scope for v1 (noted in the UI tooltip: "calendar time").
- `AgentAnalyticsView.vue` switches its first-response/response cards to these values. Queue time uses `picked_up_at - transferred_at`.

### 4.3 API
| Method & path | Report | Permission |
|---|---|---|
| `GET /api/reports/contacts-by-source?from&to&interval&split=lifecycle` | R1 | `reports:read` |
| `GET /api/reports/lifecycle-funnel?from&to` | R2 | `reports:read` |
| `GET /api/reports/pipeline-funnel?pipeline_id&from&to` | R3 funnel + win rate + time in stage | `reports:read` + `deals:read` |
| `GET /api/reports/pipeline-forecast?pipeline_id&months=6` | R3 forecast | `reports:read` + `deals:read` |
| `GET /api/reports/tasks-by-agent?from&to&team_id&type` | R4 | `reports:read` (agents: own row only via existing analytics rule) |
| `GET /api/reports/agent-performance?from&to&team_id` | R5 | `analytics.agents:read` (existing) |
| `GET /api/reports/{key}/export.csv?…` | CSV of any report | same as report |
| `GET /api/widgets/data-sources` (existing) | now includes new sources/dimensions/measures | existing |

All accept `from`/`to` as dates interpreted in the org timezone (F9). Default range: last 30 days.

### 4.4 Frontend
- **Navigation:** Analytics section → **CRM Reports** (`/analytics/crm`, icon `PieChart`, `permission: 'reports'`); update `navigationOrder`.
- **Page** `views/analytics/CrmReportsView.vue`:
  - **Header:** `DateRangePicker` (existing `useDateRange`), team filter, "Export" menu per tab.
  - **Tabs:**
    - **Overview:** R1 stacked bar + source table; R2 funnel.
    - **Agents:** R5 leaderboard + R4 tasks leaderboard.
    - **Pipeline:** R3 funnel, win rate, time in stage, forecast, lost reasons. Shown when the pipeline feature is enabled.
  - **Charts:** `vue-chartjs` (already registered in `src/lib/charts.ts`). The funnel is a horizontal bar with conversion labels between bars. Leaderboards use `DataTable` with sortable columns and inline mini-bars.
  - **"Pin to dashboard"** on each card creates a widget with the corresponding source + display type (`funnel`/`leaderboard`/chart) through the existing widgets API.
  - **Empty states** explain prerequisites ("Set a source on contacts to see this report").
- **Dashboard** (`DashboardView.vue`): widget editor lists the new data sources/measures and renders `funnel` and `leaderboard` types.
- **Accessibility:** every chart has a data table toggle; colours follow the existing dashboard palette with sufficient contrast; the legend is not colour-only (labels + values).

## 5. Migration & rollout
1. **No new tables** (optional daily rollups only if performance requires, see risks).
2. **Indexes** to verify/add:
   - `conversations (organization_id, first_response_at)`
   - `conversations (organization_id, resolved_at)`
   - `tasks (organization_id, owner_id, status, due_at)` (04)
   - `contact_activities (organization_id, type, occurred_at)` (F3)
   - `deal_stage_history (organization_id, pipeline_id, moved_at)` (07)
3. F1 permission `reports:read` for admin/manager.
4. **Shipping order:**
   - R5 quick win with 03 (endpoint + AgentAnalytics fields)
   - `internal/reports` refactor + R1/R2/R4 after 04
   - R3 after 07

## 6. Milestones
| # | Deliverable | Est. |
|---|---|---|
| M0 (quick win, in 03) | R5 agent performance from conversations; fix AgentAnalytics first-response/queue time | 2 d |
| M1 | `internal/reports` registry refactor with existing widget parity tests; timezone bucketing; cache | 3 d |
| M2 | New data sources (contacts ext., lifecycle_changes, conversations, tasks) + funnel/leaderboard display types | 2.5 d |
| M3 | R1, R2, R4 endpoints + CSV export | 1.5 d |
| M4 | CRM Reports page (Overview, Agents) + pin to dashboard + widget editor support | 3 d |
| M5 | R3 pipeline reports (after 07) + Pipeline tab | 2 d |

## 7. Testing
- **Parity:** snapshot tests of existing widget endpoints before/after the refactor (same JSON for seeded data).
- **Correctness fixtures:**
  - R2 with forward/backward stage moves
  - R5 median/p90 against hand-computed values
  - R4 on-time rate across a timezone boundary
  - R3 win rate excluding open deals
- **Timezone:** day buckets for `Asia/Karachi` vs UTC produce correct counts at midnight boundaries.
- **Scope:** an agent calling tasks-by-agent sees only their own row; an agent without `reports:read` gets 403 on CRM reports.
- **Performance:** each report < 1.5 s on a seeded org with 500k contacts, 2M messages, 200k conversations, 50k tasks, 20k deals (cold cache).
- **E2E** (`e2e/tests/analytics/crm-reports.spec.ts`): the page loads with demo data, changing the date range updates charts, CSV export downloads, pin to dashboard adds a widget.

## 8. Risks & open questions
| Risk / question | Mitigation / proposal |
|---|---|
| Heavy percentile queries on large orgs | Redis cache; indexes; if p95 > 1.5 s, add nightly `daily_agent_metrics` / `daily_contact_metrics` rollups (F4 job) and read historic days from rollups + today live |
| Contacts without source skew R1 | Optional source backfill heuristic in 01; "Unknown" bucket explicit |
| Business-hours-aware response times expected | v1 calendar time with tooltip; later compute using chatbot business hours per org |
| Funnel semantics debated (reached vs currently in) | "Reached" chosen (standard CRM funnel); documented in UI info tooltip |

## 9. Acceptance criteria
- [ ] CRM Reports shows new contacts by source over time, a lifecycle funnel with step conversion, tasks per agent (open/overdue/on-time) and agent performance (median/p90 first response and resolution) for any date range in the org timezone.
- [ ] With pipelines enabled, the Pipeline tab shows the stage funnel, win rate, time in stage, forecast and lost reasons.
- [ ] Agent Analytics first-response and queue-time values are populated from real data.
- [ ] Each report card can be exported to CSV and pinned to the dashboard as a widget; the widget editor offers the new data sources, including funnel and leaderboard types.
- [ ] Existing dashboard widgets return identical data after the query-layer refactor.


---

## Integration addendum (from [10 — System integration](10-system-integration.md))

- **Agent metrics:**
  - Response metrics count only `messages.sender_type = 'agent'`; bot, system, API, echo, campaign and automation sends are excluded (S4).
  - First response runs from the first customer message to the first successful agent send.
- **Timezone:** all `from` / `to` ranges and time buckets use the org timezone (S11). Today, browser-local dates are parsed as UTC.
- **Widgets:**
  - `executeWidgetQuery` gains the viewer, for "me" widgets and phone masking. The existing table widget leaks raw phone numbers (X8).
  - Pinned report widgets check `reports:read` at render time.
  - Agents get Home widgets (My conversations, My tasks), since they cannot open the Dashboard.
  - New default widgets reach existing orgs through a migration, because `SeedDefaultWidgets` skips any org that already has widgets.
- **New report:** campaign reply rate and leads created from campaign replies (uses `origin_campaign_id` / `campaign.replied`).
- **Catalog:** widget data sources, dimensions and display types come from the backend catalog. `funnel` / `leaderboard` are added to the whitelists (`widgets.go:121-137`).
