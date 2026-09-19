package handlers

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
)

// Measures: the things a dashboard can show, named the way the person reading
// the dashboard would ask for them.
//
// The original builder asked for a table, a metric and a column — "data source:
// transfers, metric: avg, field: resolution_time" — and took filter values as
// free text, so narrowing a widget to one agent meant typing that agent's UUID.
// That is a query language, not a dashboard. A measure is the question itself
// ("Waiting for a reply", "Deals won"), with everything a query needs decided
// here, once: the table, the timestamp the date range applies to (or none, for
// a count of right now), the fixed conditions, and the ways it can be split
// and narrowed. The builder offers measures; a person picks one, picks how to
// show it, and optionally narrows it with pickers.
//
// Every SQL fragment in this file is a constant. What a widget stores — the
// measure key, the split key, filter keys — only ever selects between them, and
// filter values are always bound parameters.

// dimKind says what a dimension's values are, which decides how they are
// labelled and offered.
type dimKind string

const (
	dimUser       dimKind = "user"       // users.id
	dimTeam       dimKind = "team"       // teams.id
	dimStage      dimKind = "stage"      // pipeline_stages.id
	dimPipeline   dimKind = "pipeline"   // pipelines.id
	dimTaskType   dimKind = "task_type"  // task_types.id
	dimAutomation dimKind = "automation" // automation_rules.id
	dimFlow       dimKind = "flow"       // chatbot_flows.id
	dimAccount    dimKind = "account"    // a WhatsApp account's name
	dimField      dimKind = "field"      // an option of a contact field (Field names it)
	dimEnum       dimKind = "enum"       // one of Values
	dimText       dimKind = "text"       // free text; split only, never offered as a filter
)

// idKinds are the dimensions whose values are UUIDs, bound as such.
var idKinds = map[dimKind]bool{
	dimUser: true, dimTeam: true, dimStage: true, dimPipeline: true,
	dimTaskType: true, dimAutomation: true, dimFlow: true,
}

// measureDim is one way a measure can be split or narrowed.
type measureDim struct {
	Key    string   // what a widget stores in group_by_field and filters[].field
	Column string   // SQL over the measure's alias
	Kind   dimKind  //
	Values []string // the choices, for an enum
	Field  string   // the contact field whose options label the values, for a field
}

// widgetMeasure is one thing a dashboard can show.
type widgetMeasure struct {
	Key        string
	Area       string // inbox, contacts, deals, followups, automations, calls, messaging, chatbot
	Table      string
	Alias      string
	SoftDelete bool
	// When is the timestamp the date range applies to. Empty means the
	// measure is a count of right now — open, overdue, waiting — which the
	// date range does not change.
	When  string
	Where string // fixed conditions, joined with AND
	// Value is the aggregate. Empty means COUNT(*).
	Value string
	Unit  string // "", "money", "minutes" or "seconds"
	// LowerIsBetter turns a rise red: more overdue follow-ups is bad news.
	LowerIsBetter bool
	Dims          []measureDim
	// Person is the dimension a leaderboard ranks; empty means none.
	Person string
	// Funnel serves the measure from the report that computes it.
	Funnel string // "lifecycle" or "pipeline"
}

// Snapshot reports whether the measure counts what is true right now.
func (m *widgetMeasure) Snapshot() bool { return m.When == "" && m.Funnel == "" }

// Views are the ways the measure can be drawn, in the order the builder
// offers them.
func (m *widgetMeasure) Views() []string {
	if m.Funnel != "" {
		return []string{"funnel"}
	}
	views := []string{"number"}
	if m.When != "" {
		views = append(views, "trend")
	}
	if len(m.Dims) > 0 {
		views = append(views, "bar", "pie", "table")
	}
	if m.Person != "" {
		views = append(views, "leaderboard")
	}
	return views
}

// Dim looks up a dimension by key.
func (m *widgetMeasure) Dim(key string) *measureDim {
	for i := range m.Dims {
		if m.Dims[i].Key == key {
			return &m.Dims[i]
		}
	}
	return nil
}

// viewDisplay maps a view to the display and chart type the widget row keeps,
// so the columns stay meaningful to anything that reads them directly.
var viewDisplay = models.WidgetViewDisplay

// areaColor is the tile colour a measure's number card wears, by area, so a
// person building a widget has one decision fewer to make.
var areaColor = map[string]string{
	"inbox": "blue", "contacts": "green", "deals": "green", "followups": "orange",
	"automations": "purple", "calls": "cyan", "messaging": "blue", "chatbot": "purple",
}

// --- The dimensions, per table ---

func enumDim(key, column string, values ...string) measureDim {
	return measureDim{Key: key, Column: column, Kind: dimEnum, Values: values}
}

var (
	convStatus   = enumDim("status", "cv.status", "open", "pending", "snoozed", "resolved")
	convAssignee = measureDim{Key: "assignee", Column: "cv.assignee_id", Kind: dimUser}
	convTeam     = measureDim{Key: "team", Column: "cv.team_id", Kind: dimTeam}
	convAccount  = measureDim{Key: "account", Column: "cv.whatsapp_account", Kind: dimAccount}
	convReason   = enumDim("reason", "cv.resolution_reason",
		models.ResolutionAgent, models.ResolutionSLAAutoClose, models.ResolutionClientInactivity,
		models.ResolutionPendingTimeout, models.ResolutionMerged, models.ResolutionBulk,
		models.ResolutionAutomation, models.ResolutionContactDeleted)
	convResolver  = measureDim{Key: "resolver", Column: "cv.resolved_by_id", Kind: dimUser}
	convResponder = measureDim{Key: "responder", Column: "cv.first_responder_id", Kind: dimUser}

	contactSource = measureDim{Key: "source", Column: "ct.source", Kind: dimField, Field: "source"}
	contactOwner  = measureDim{Key: "owner", Column: "ct.assigned_user_id", Kind: dimUser}
	// These tables name the column whats_app_account (GORM's spelling of the
	// field); conversations and call logs set it to whatsapp_account.
	contactAcct = measureDim{Key: "account", Column: "ct.whats_app_account", Kind: dimAccount}
	// The lifecycle stage is a field value, not a column on the contact.
	contactStage = measureDim{Key: "lifecycle", Kind: dimField, Field: models.FieldKeyLifecycleStage, Column: fmt.Sprintf(
		`(SELECT v.value_option FROM custom_field_values v
		   JOIN custom_field_definitions fd ON fd.id = v.field_id
		  WHERE v.entity_type = '%s' AND v.entity_id = ct.id
		    AND fd.organization_id = ct.organization_id AND fd.key = '%s'
		  LIMIT 1)`, models.FieldEntityContact, models.FieldKeyLifecycleStage)}

	dealStage    = measureDim{Key: "stage", Column: "d.stage_id", Kind: dimStage}
	dealOwner    = measureDim{Key: "owner", Column: "d.owner_id", Kind: dimUser}
	dealPipeline = measureDim{Key: "pipeline", Column: "d.pipeline_id", Kind: dimPipeline}
	dealReason   = measureDim{Key: "reason", Column: "NULLIF(d.lost_reason, '')", Kind: dimText}

	taskOwner    = measureDim{Key: "owner", Column: "tk.owner_id", Kind: dimUser}
	taskType     = measureDim{Key: "type", Column: "tk.type_id", Kind: dimTaskType}
	taskPriority = enumDim("priority", "tk.priority", models.TaskPriorityHigh, models.TaskPriorityNormal, models.TaskPriorityLow)
	taskSource   = enumDim("source", "tk.source",
		models.TaskSourceManual, models.TaskSourceAutomation, models.TaskSourceChatbot,
		models.TaskSourceCall, models.TaskSourceAPI)

	runAutomation  = measureDim{Key: "automation", Column: "ar.rule_id", Kind: dimAutomation}
	runStatus      = enumDim("status", "ar.status", models.AutomationSucceeded, models.AutomationPartiallyFailed, models.AutomationFailed, models.AutomationSkipped, models.AutomationWaiting, models.AutomationCancelled)
	waitAutomation = measureDim{Key: "automation", Column: "aw.rule_id", Kind: dimAutomation}

	callDirection = enumDim("direction", "cl.direction", string(models.CallDirectionIncoming), string(models.CallDirectionOutgoing))
	callStatus    = enumDim("status", "cl.status",
		string(models.CallStatusCompleted), string(models.CallStatusMissed), string(models.CallStatusRejected),
		string(models.CallStatusFailed), string(models.CallStatusAnswered))
	callAgent   = measureDim{Key: "agent", Column: "cl.agent_id", Kind: dimUser}
	callAccount = measureDim{Key: "account", Column: "cl.whatsapp_account", Kind: dimAccount}

	msgAccount = measureDim{Key: "account", Column: "ms.whats_app_account", Kind: dimAccount}
	msgType    = enumDim("type", "ms.message_type",
		string(models.MessageTypeText), string(models.MessageTypeTemplate), string(models.MessageTypeImage),
		string(models.MessageTypeDocument), string(models.MessageTypeAudio), string(models.MessageTypeVideo),
		string(models.MessageTypeInteractive))
	msgStatus = enumDim("status", "ms.status",
		string(models.MessageStatusSent), string(models.MessageStatusDelivered),
		string(models.MessageStatusRead), string(models.MessageStatusFailed))
	msgSender = measureDim{Key: "sender", Column: "ms.sent_by_user_id", Kind: dimUser}

	sessionStatus = enumDim("status", "cs.status",
		string(models.SessionStatusActive), string(models.SessionStatusCompleted),
		string(models.SessionStatusCancelled), string(models.SessionStatusTimeout))
	sessionFlow    = measureDim{Key: "flow", Column: "cs.current_flow_id", Kind: dimFlow}
	sessionAccount = measureDim{Key: "account", Column: "cs.whats_app_account", Kind: dimAccount}

	handoffSource = enumDim("source", "ht.source",
		string(models.TransferSourceManual), string(models.TransferSourceFlow), string(models.TransferSourceKeyword),
		string(models.TransferSourceChatbotDisabled), string(models.TransferSourceAutomation))
	handoffTeam  = measureDim{Key: "team", Column: "ht.team_id", Kind: dimTeam}
	handoffAgent = measureDim{Key: "agent", Column: "ht.agent_id", Kind: dimUser}
)

// --- The measures ---

// firstReplyMinutes is how long a customer waited for the first answer. An
// agent who wrote first has not kept anyone waiting, so it never goes negative.
const firstReplyMinutes = `AVG(GREATEST(EXTRACT(EPOCH FROM (cv.first_response_at - COALESCE(cv.first_customer_message_at, cv.opened_at))), 0) / 60.0)`

var widgetMeasures = []widgetMeasure{
	// Inbox: the work that is waiting, and how quickly it is met.
	{Key: "conversations_waiting", Area: "inbox", Table: "conversations", Alias: "cv", SoftDelete: true,
		Where: "cv.status = 'open' AND cv.waiting_since IS NOT NULL", LowerIsBetter: true,
		Dims: []measureDim{convAssignee, convTeam, convAccount}, Person: "assignee"},
	{Key: "conversations_unassigned", Area: "inbox", Table: "conversations", Alias: "cv", SoftDelete: true,
		Where: "cv.status IN ('open', 'pending') AND cv.assignee_id IS NULL", LowerIsBetter: true,
		Dims: []measureDim{convTeam, convAccount}},
	{Key: "conversations_open", Area: "inbox", Table: "conversations", Alias: "cv", SoftDelete: true,
		Where: "cv.status IN ('open', 'pending')",
		Dims:  []measureDim{convStatus, convAssignee, convTeam, convAccount}, Person: "assignee"},
	{Key: "conversations_started", Area: "inbox", Table: "conversations", Alias: "cv", SoftDelete: true,
		When: "cv.opened_at",
		Dims: []measureDim{convAccount, convTeam, convAssignee}, Person: "assignee"},
	{Key: "conversations_resolved", Area: "inbox", Table: "conversations", Alias: "cv", SoftDelete: true,
		When: "cv.resolved_at", Where: "cv.resolved_at IS NOT NULL",
		Dims: []measureDim{convReason, convResolver, convTeam, convAccount}, Person: "resolver"},
	{Key: "first_reply_time", Area: "inbox", Table: "conversations", Alias: "cv", SoftDelete: true,
		When: "cv.first_response_at", Where: "cv.first_response_at IS NOT NULL",
		Value: firstReplyMinutes, Unit: "minutes", LowerIsBetter: true,
		Dims: []measureDim{convResponder, convTeam, convAccount}, Person: "responder"},

	// Contacts: who is arriving, from where, and how far along they are.
	{Key: "contacts_new", Area: "contacts", Table: "contacts", Alias: "ct", SoftDelete: true,
		When: "ct.created_at", Where: "ct.merged_into_id IS NULL",
		Dims: []measureDim{contactSource, contactStage, contactOwner, contactAcct}, Person: "owner"},
	{Key: "contacts_active", Area: "contacts", Table: "contacts", Alias: "ct", SoftDelete: true,
		When: "ct.last_inbound_at", Where: "ct.merged_into_id IS NULL",
		Dims: []measureDim{contactStage, contactSource, contactOwner, contactAcct}, Person: "owner"},
	{Key: "contacts_all", Area: "contacts", Table: "contacts", Alias: "ct", SoftDelete: true,
		Where: "ct.merged_into_id IS NULL",
		Dims:  []measureDim{contactStage, contactSource, contactOwner, contactAcct}, Person: "owner"},
	{Key: "lifecycle_funnel", Area: "contacts", Funnel: "lifecycle"},

	// Deals: what is on the board, and what closed.
	{Key: "deals_open_value", Area: "deals", Table: "deals", Alias: "d", SoftDelete: true,
		Where: "d.status = 'open'", Value: "SUM(d.value)", Unit: "money",
		Dims: []measureDim{dealStage, dealOwner, dealPipeline}, Person: "owner"},
	{Key: "deals_open", Area: "deals", Table: "deals", Alias: "d", SoftDelete: true,
		Where: "d.status = 'open'",
		Dims:  []measureDim{dealStage, dealOwner, dealPipeline}, Person: "owner"},
	{Key: "deals_won_value", Area: "deals", Table: "deals", Alias: "d", SoftDelete: true,
		When: "d.closed_at", Where: "d.status = 'won'", Value: "SUM(d.value)", Unit: "money",
		Dims: []measureDim{dealOwner, dealPipeline}, Person: "owner"},
	{Key: "deals_won", Area: "deals", Table: "deals", Alias: "d", SoftDelete: true,
		When: "d.closed_at", Where: "d.status = 'won'",
		Dims: []measureDim{dealOwner, dealPipeline}, Person: "owner"},
	{Key: "deals_lost", Area: "deals", Table: "deals", Alias: "d", SoftDelete: true,
		When: "d.closed_at", Where: "d.status = 'lost'", LowerIsBetter: true,
		Dims: []measureDim{dealReason, dealOwner, dealPipeline}, Person: "owner"},
	{Key: "deals_new", Area: "deals", Table: "deals", Alias: "d", SoftDelete: true,
		When: "d.created_at",
		Dims: []measureDim{dealStage, dealOwner, dealPipeline}, Person: "owner"},
	{Key: "pipeline_funnel", Area: "deals", Funnel: "pipeline"},

	// Follow-ups: what is owed, what is late, what got done.
	{Key: "tasks_overdue", Area: "followups", Table: "tasks", Alias: "tk", SoftDelete: true,
		Where: "tk.status = 'open' AND tk.due_at < NOW()", LowerIsBetter: true,
		Dims: []measureDim{taskOwner, taskType, taskPriority}, Person: "owner"},
	{Key: "tasks_open", Area: "followups", Table: "tasks", Alias: "tk", SoftDelete: true,
		Where: "tk.status = 'open'",
		Dims:  []measureDim{taskOwner, taskType, taskPriority}, Person: "owner"},
	{Key: "tasks_completed", Area: "followups", Table: "tasks", Alias: "tk", SoftDelete: true,
		When: "tk.completed_at", Where: "tk.status = 'completed'",
		Dims: []measureDim{taskOwner, taskType, taskPriority}, Person: "owner"},
	{Key: "tasks_created", Area: "followups", Table: "tasks", Alias: "tk", SoftDelete: true,
		When: "tk.created_at",
		Dims: []measureDim{taskSource, taskType, taskOwner, taskPriority}, Person: "owner"},

	// Automations: whether they are doing their job.
	{Key: "automation_runs", Area: "automations", Table: "automation_runs", Alias: "ar",
		When: "ar.started_at", Where: "ar.dry_run = false",
		Dims: []measureDim{runAutomation, runStatus}},
	{Key: "automation_failures", Area: "automations", Table: "automation_runs", Alias: "ar",
		When: "ar.started_at", Where: "ar.dry_run = false AND ar.status IN ('failed', 'partially_failed')", LowerIsBetter: true,
		Dims: []measureDim{runAutomation}},
	{Key: "automation_waiting", Area: "automations", Table: "automation_waits", Alias: "aw",
		Where: "aw.status = 'pending'",
		Dims:  []measureDim{waitAutomation}},

	// Calls.
	{Key: "calls_total", Area: "calls", Table: "call_logs", Alias: "cl", SoftDelete: true,
		When: "cl.created_at",
		Dims: []measureDim{callDirection, callStatus, callAgent, callAccount}, Person: "agent"},
	{Key: "calls_missed", Area: "calls", Table: "call_logs", Alias: "cl", SoftDelete: true,
		When: "cl.created_at", Where: "cl.direction = 'incoming' AND cl.status IN ('missed', 'rejected')", LowerIsBetter: true,
		Dims: []measureDim{callAccount, callAgent}},
	{Key: "call_length", Area: "calls", Table: "call_logs", Alias: "cl", SoftDelete: true,
		When: "cl.created_at", Where: "cl.duration > 0", Value: "AVG(cl.duration)", Unit: "seconds",
		Dims: []measureDim{callDirection, callAgent, callAccount}, Person: "agent"},

	// Messaging.
	{Key: "messages_received", Area: "messaging", Table: "messages", Alias: "ms", SoftDelete: true,
		When: "ms.created_at", Where: "ms.direction = 'incoming'",
		Dims: []measureDim{msgAccount, msgType}},
	{Key: "messages_sent", Area: "messaging", Table: "messages", Alias: "ms", SoftDelete: true,
		When: "ms.created_at", Where: "ms.direction = 'outgoing'",
		Dims: []measureDim{msgSender, msgStatus, msgType, msgAccount}, Person: "sender"},
	{Key: "messages_failed", Area: "messaging", Table: "messages", Alias: "ms", SoftDelete: true,
		When: "ms.created_at", Where: "ms.direction = 'outgoing' AND ms.status = 'failed'", LowerIsBetter: true,
		Dims: []measureDim{msgAccount, msgType, msgSender}},

	// Chatbot, and when it hands over to a person.
	{Key: "chatbot_sessions", Area: "chatbot", Table: "chatbot_sessions", Alias: "cs", SoftDelete: true,
		When: "cs.created_at",
		Dims: []measureDim{sessionStatus, sessionFlow, sessionAccount}},
	{Key: "handoffs", Area: "chatbot", Table: "agent_transfers", Alias: "ht", SoftDelete: true,
		When: "ht.transferred_at",
		Dims: []measureDim{handoffSource, handoffTeam, handoffAgent}, Person: "agent"},
	{Key: "handoffs_late", Area: "chatbot", Table: "agent_transfers", Alias: "ht", SoftDelete: true,
		When: "ht.transferred_at", Where: "ht.sla_breached = true", LowerIsBetter: true,
		Dims: []measureDim{handoffTeam, handoffAgent, handoffSource}, Person: "agent"},
}

var measuresByKey = func() map[string]*widgetMeasure {
	out := make(map[string]*widgetMeasure, len(widgetMeasures))
	for i := range widgetMeasures {
		out[widgetMeasures[i].Key] = &widgetMeasures[i]
	}
	return out
}()

// measureKey returns the measure a widget config names, or "".
func measureKey(config map[string]any) string {
	key, _ := config["measure"].(string)
	return key
}

// measureOf returns the measure a widget is built on, or nil for a widget
// from the original builder.
func measureOf(w models.Widget) *widgetMeasure {
	return measuresByKey[measureKey(w.Config)]
}

// normalizeMeasureWidget checks a widget built on a measure and fills in the
// columns the rest of the widget code reads. It returns a message for anything
// that cannot be saved.
func normalizeMeasureWidget(req *WidgetRequest) string {
	m := measuresByKey[measureKey(req.Config)]
	if m == nil {
		return "Unknown measure"
	}

	view, _ := req.Config["view"].(string)
	if view == "" {
		view = m.Views()[0]
	}
	if !contains(m.Views(), view) {
		return "This measure cannot be shown that way"
	}
	display := viewDisplay[view]
	req.DisplayType, req.ChartType = display[0], display[1]
	req.DataSource = "measure"
	req.Metric = "count"
	req.Field = ""

	switch view {
	case "bar", "pie", "table":
		if m.Dim(req.GroupByField) == nil {
			return "Choose what to split it by"
		}
	case "leaderboard":
		// A leaderboard ranks people; any person dimension will do, the
		// measure's own by default.
		if d := m.Dim(req.GroupByField); d == nil || d.Kind != dimUser {
			req.GroupByField = m.Person
		}
	default:
		req.GroupByField = ""
	}

	filters := make([]FilterInput, 0, len(req.Filters))
	for _, f := range req.Filters {
		d := m.Dim(f.Field)
		if d == nil || d.Kind == dimText {
			return "A filter names something this measure cannot be narrowed by"
		}
		if f.Operator != "equals" && f.Operator != "not_equals" {
			return "A filter can only say \"is\" or \"is not\""
		}
		if strings.TrimSpace(f.Value) == "" {
			return "A filter is missing its value"
		}
		filters = append(filters, f)
	}
	req.Filters = filters

	if req.Color == "" {
		req.Color = areaColor[m.Area]
	}
	config := map[string]any{"measure": m.Key, "view": view}
	if m.Funnel != "" {
		config["funnel"] = m.Funnel
	}
	req.Config = config
	// A count of right now has no earlier period to compare with.
	showChange := view == "number" && !m.Snapshot()
	req.ShowChange = &showChange
	return ""
}

// --- Running a measure ---

// measureQuery assembles "FROM … WHERE …" for a measure and a widget's
// filters, with the period applied when the measure has one.
func (a *App) measureQuery(orgID, viewerID uuid.UUID, m *widgetMeasure, filters []FilterInput, start, end *time.Time) (string, []any) {
	var b strings.Builder
	fmt.Fprintf(&b, " FROM %s %s WHERE %s.organization_id = ?", m.Table, m.Alias, m.Alias)
	args := []any{orgID}
	if m.SoftDelete {
		fmt.Fprintf(&b, " AND %s.deleted_at IS NULL", m.Alias)
	}
	if m.Where != "" {
		fmt.Fprintf(&b, " AND (%s)", m.Where)
	}
	if m.When != "" && start != nil && end != nil {
		fmt.Fprintf(&b, " AND %s >= ? AND %s <= ?", m.When, m.When)
		args = append(args, *start, *end)
	}
	for _, f := range filters {
		d := m.Dim(f.Field)
		if d == nil || d.Kind == dimText {
			continue
		}
		value := f.Value
		if value == "me" && d.Kind == dimUser {
			value = viewerID.String()
		}
		cast := "text"
		if idKinds[d.Kind] {
			if _, err := uuid.Parse(value); err != nil {
				continue
			}
			cast = "uuid"
		}
		op := "IS NOT DISTINCT FROM"
		if f.Operator == "not_equals" {
			op = "IS DISTINCT FROM"
		}
		fmt.Fprintf(&b, " AND (%s) %s CAST(? AS %s)", d.Column, op, cast)
		args = append(args, value)
	}
	return b.String(), args
}

func (m *widgetMeasure) aggregate() string {
	if m.Value == "" {
		return "COUNT(*)"
	}
	return "COALESCE(" + m.Value + ", 0)"
}

// measureValue computes the measure's single number.
func (a *App) measureValue(orgID, viewerID uuid.UUID, m *widgetMeasure, filters []FilterInput, start, end *time.Time) (float64, error) {
	from, args := a.measureQuery(orgID, viewerID, m, filters, start, end)
	var value float64
	err := a.DB.Raw("SELECT "+m.aggregate()+" AS value"+from, args...).Scan(&value).Error
	return value, err
}

// measureChange is the percentage move from the previous period.
func measureChange(previous, current float64) float64 {
	if previous == 0 {
		if current == 0 {
			return 0
		}
		return 100
	}
	return (current - previous) / previous * 100
}

// trendBucket picks how finely a trend is drawn: by day for a month, by week
// for a quarter, by month beyond that — a year of daily points is a smear.
func trendBucket(start, end time.Time) string {
	days := end.Sub(start).Hours() / 24
	switch {
	case days > 400:
		return "month"
	case days > 92:
		return "week"
	default:
		return "day"
	}
}

// measureTrend computes the measure per day (or week, or month) over the
// period, in the organization's own days, with empty days drawn as zero.
func (a *App) measureTrend(orgID, viewerID uuid.UUID, m *widgetMeasure, filters []FilterInput, start, end time.Time) ([]ChartPoint, error) {
	loc := a.OrgLocation(orgID)
	bucket := trendBucket(start, end)
	from, args := a.measureQuery(orgID, viewerID, m, filters, &start, &end)
	query := fmt.Sprintf("SELECT DATE_TRUNC('%s', %s AT TIME ZONE ?) AS bucket, %s AS value%s GROUP BY 1 ORDER BY 1",
		bucket, m.When, m.aggregate(), from)
	type row struct {
		Bucket time.Time
		Value  float64
	}
	var rows []row
	if err := a.DB.Raw(query, append([]any{loc.String()}, args...)...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	byBucket := make(map[string]float64, len(rows))
	for _, r := range rows {
		byBucket[r.Bucket.Format("2006-01-02")] = r.Value
	}

	// Walk the period in the organization's calendar so a quiet day is a
	// zero on the line rather than a gap the line jumps across.
	local := start.In(loc)
	cursor := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	switch bucket {
	case "week":
		offset := (int(cursor.Weekday()) + 6) % 7 // Postgres weeks start on Monday
		cursor = cursor.AddDate(0, 0, -offset)
	case "month":
		cursor = time.Date(cursor.Year(), cursor.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	endLocal := end.In(loc)
	last := time.Date(endLocal.Year(), endLocal.Month(), endLocal.Day(), 0, 0, 0, 0, time.UTC)
	points := make([]ChartPoint, 0, 32)
	for !cursor.After(last) && len(points) < 400 {
		label := cursor.Format("Jan 02")
		if bucket == "month" {
			label = cursor.Format("Jan 2006")
		}
		points = append(points, ChartPoint{Label: label, Value: byBucket[cursor.Format("2006-01-02")]})
		switch bucket {
		case "week":
			cursor = cursor.AddDate(0, 0, 7)
		case "month":
			cursor = cursor.AddDate(0, 1, 0)
		default:
			cursor = cursor.AddDate(0, 0, 1)
		}
	}
	return points, nil
}

// measureSplit computes the measure per value of one dimension.
func (a *App) measureSplit(orgID, viewerID uuid.UUID, m *widgetMeasure, d *measureDim, filters []FilterInput, start, end *time.Time, ranking bool) ([]DataPoint, error) {
	from, args := a.measureQuery(orgID, viewerID, m, filters, start, end)
	order, limit := "DESC", 25
	if ranking {
		limit = 10
		// A time is ranked quickest first; a count most first.
		if m.LowerIsBetter && (m.Unit == "minutes" || m.Unit == "seconds") {
			order = "ASC"
		}
		// A ranking of people has no place for "nobody".
		from += fmt.Sprintf(" AND (%s) IS NOT NULL", d.Column)
	}
	query := fmt.Sprintf("SELECT (%s)::text AS k, %s AS value%s GROUP BY 1 ORDER BY 2 %s LIMIT %d",
		d.Column, m.aggregate(), from, order, limit)
	type row struct {
		K     *string
		Value float64
	}
	var rows []row
	if err := a.DB.Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.K != nil {
			keys = append(keys, *r.K)
		}
	}
	names := a.dimLabels(orgID, d, keys)

	out := make([]DataPoint, 0, len(rows))
	for _, r := range rows {
		key := ""
		if r.K != nil {
			key = *r.K
		}
		label := key
		if name, ok := names[key]; ok {
			label = name
		}
		out = append(out, DataPoint{Key: key, Label: label, Value: r.Value})
	}
	return out, nil
}

// dimLabelSQL says where the name for a referenced row lives.
var dimLabelSQL = map[dimKind]struct {
	table, name string
	global      bool
}{
	dimUser:       {"users", "full_name", true},
	dimTeam:       {"teams", "name", false},
	dimStage:      {"pipeline_stages", "name", false},
	dimPipeline:   {"pipelines", "name", false},
	dimTaskType:   {"task_types", "label", false},
	dimAutomation: {"automation_rules", "name", false},
	dimFlow:       {"chatbot_flows", "name", false},
}

// dimLabels names the values a split came back with. A reference is swapped
// for the name it points at; a field option for its label. Enums are left for
// the interface to put into words, in the viewer's language.
func (a *App) dimLabels(orgID uuid.UUID, d *measureDim, keys []string) map[string]string {
	if len(keys) == 0 {
		return nil
	}
	if d.Kind == dimField {
		out := map[string]string{}
		for _, o := range a.fieldOptions(orgID, d.Field) {
			out[o.Value] = o.Label
		}
		return out
	}
	src, ok := dimLabelSQL[d.Kind]
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(keys))
	for _, k := range keys {
		if _, err := uuid.Parse(k); err == nil {
			ids = append(ids, k)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	type row struct {
		ID   string
		Name string
	}
	var rows []row
	q := fmt.Sprintf("SELECT id::text AS id, %s AS name FROM %s WHERE id::text IN ?", src.name, src.table)
	if src.global {
		a.DB.Raw(q, ids).Scan(&rows)
	} else {
		a.DB.Raw(q+" AND organization_id = ?", ids, orgID).Scan(&rows)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		if r.Name != "" {
			out[r.ID] = r.Name
		}
	}
	return out
}

// dimOption is one choice a filter offers.
type dimOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// fieldOptions lists a contact field's options, in the organization's order.
func (a *App) fieldOptions(orgID uuid.UUID, key string) []dimOption {
	var def models.CustomFieldDefinition
	if err := a.DB.Where("organization_id = ? AND entity_type = ? AND key = ?", orgID, models.FieldEntityContact, key).
		First(&def).Error; err != nil {
		return nil
	}
	out := make([]dimOption, 0, len(def.Options))
	for _, raw := range def.Options {
		option, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		value, _ := option["value"].(string)
		label, _ := option["label"].(string)
		if value == "" {
			continue
		}
		if label == "" {
			label = value
		}
		out = append(out, dimOption{Value: value, Label: label})
	}
	return out
}

// kindOptions lists what an organization's filters of one kind can choose
// between. Users come without "me"; the builder adds it, in the viewer's words.
func (a *App) kindOptions(orgID uuid.UUID, kind dimKind) []dimOption {
	type row struct {
		Value string
		Label string
	}
	var rows []row
	switch kind {
	case dimUser:
		a.DB.Raw(`SELECT u.id::text AS value, u.full_name AS label FROM users u
			JOIN user_organizations uo ON uo.user_id = u.id AND uo.organization_id = ? AND uo.deleted_at IS NULL
			WHERE u.deleted_at IS NULL ORDER BY u.full_name`, orgID).Scan(&rows)
	case dimTeam:
		a.DB.Raw(`SELECT id::text AS value, name AS label FROM teams
			WHERE organization_id = ? AND deleted_at IS NULL ORDER BY name`, orgID).Scan(&rows)
	case dimStage:
		// A stage is named with its pipeline when there is more than one, so
		// two "Qualified" stages can be told apart.
		a.DB.Raw(`SELECT s.id::text AS value,
				CASE WHEN (SELECT COUNT(*) FROM pipelines p2 WHERE p2.organization_id = ? AND p2.deleted_at IS NULL) > 1
				     THEN p.name || ' › ' || s.name ELSE s.name END AS label
			FROM pipeline_stages s JOIN pipelines p ON p.id = s.pipeline_id
			WHERE s.organization_id = ? AND s.deleted_at IS NULL AND p.deleted_at IS NULL
			ORDER BY p.position, s.position`, orgID, orgID).Scan(&rows)
	case dimPipeline:
		a.DB.Raw(`SELECT id::text AS value, name AS label FROM pipelines
			WHERE organization_id = ? AND deleted_at IS NULL ORDER BY position`, orgID).Scan(&rows)
	case dimTaskType:
		a.DB.Raw(`SELECT id::text AS value, label FROM task_types
			WHERE organization_id = ? AND deleted_at IS NULL ORDER BY position, label`, orgID).Scan(&rows)
	case dimAutomation:
		a.DB.Raw(`SELECT id::text AS value, name AS label FROM automation_rules
			WHERE organization_id = ? AND deleted_at IS NULL ORDER BY name`, orgID).Scan(&rows)
	case dimFlow:
		a.DB.Raw(`SELECT id::text AS value, name AS label FROM chatbot_flows
			WHERE organization_id = ? AND deleted_at IS NULL ORDER BY name`, orgID).Scan(&rows)
	case dimAccount:
		a.DB.Raw(`SELECT name AS value, name AS label FROM whatsapp_accounts
			WHERE organization_id = ? AND deleted_at IS NULL ORDER BY name`, orgID).Scan(&rows)
	}
	out := make([]dimOption, 0, len(rows))
	for _, r := range rows {
		out = append(out, dimOption{Value: r.Value, Label: r.Label})
	}
	return out
}

// measureCurrency is what a money measure is counted in: the default
// pipeline's currency, which is what the board shows.
func (a *App) measureCurrency(orgID uuid.UUID) string {
	var currency string
	a.DB.Raw(`SELECT currency FROM pipelines WHERE organization_id = ? AND deleted_at IS NULL
		ORDER BY is_default DESC, position LIMIT 1`, orgID).Scan(&currency)
	if currency == "" {
		return "USD"
	}
	return strings.TrimSpace(currency)
}

// measureWidgetData computes a widget built on a measure.
func (a *App) measureWidgetData(orgID, viewerID uuid.UUID, w models.Widget, m *widgetMeasure, filters []FilterInput, start, end, prevStart, prevEnd time.Time) WidgetDataResponse {
	response := WidgetDataResponse{
		Unit:          m.Unit,
		LowerIsBetter: m.LowerIsBetter,
		Snapshot:      m.Snapshot(),
	}
	if m.Unit == "money" {
		response.Currency = a.measureCurrency(orgID)
	}

	if m.Funnel != "" {
		funnel := w
		funnel.Config = models.JSONB{"funnel": m.Funnel}
		response.DataPoints = a.widgetFunnelData(orgID, funnel, start, end)
		return response
	}

	// A query that fails is logged and drawn as empty rather than failing the
	// whole dashboard, which is fetched in one request.
	logged := func(err error) {
		if err != nil {
			a.Log.Error("widget measure", "measure", m.Key, "widget", w.ID, "error", err)
		}
	}
	var err error
	view, _ := w.Config["view"].(string)
	switch view {
	case "trend":
		if m.When == "" {
			return response
		}
		response.ChartData, err = a.measureTrend(orgID, viewerID, m, filters, start, end)
		logged(err)
		response.Value, err = a.measureValue(orgID, viewerID, m, filters, &start, &end)
		logged(err)
	case "bar", "pie", "table", "leaderboard":
		d := m.Dim(w.GroupByField)
		if d == nil {
			return response
		}
		response.DataPoints, err = a.measureSplit(orgID, viewerID, m, d, filters, &start, &end, view == "leaderboard")
		logged(err)
		// A split of an average is not summed back up: the total would mean
		// nothing, so the number beside a split is the measure itself.
		response.Value, err = a.measureValue(orgID, viewerID, m, filters, &start, &end)
		logged(err)
	default:
		response.Value, err = a.measureValue(orgID, viewerID, m, filters, &start, &end)
		logged(err)
		if !m.Snapshot() {
			response.PrevValue, err = a.measureValue(orgID, viewerID, m, filters, &prevStart, &prevEnd)
			logged(err)
			response.Change = measureChange(response.PrevValue, response.Value)
		}
	}
	return response
}

// --- The catalog the builder offers ---

type catalogDim struct {
	Key    string      `json:"key"`
	Kind   dimKind     `json:"kind"`
	Values []string    `json:"values,omitempty"`
	Field  string      `json:"field,omitempty"`
	Split  bool        `json:"split"`
	Filter bool        `json:"filter"`
	Person bool        `json:"person"`
	Opts   []dimOption `json:"options,omitempty"`
}

type catalogMeasure struct {
	Key           string       `json:"key"`
	Area          string       `json:"area"`
	Unit          string       `json:"unit,omitempty"`
	Snapshot      bool         `json:"snapshot"`
	LowerIsBetter bool         `json:"lower_is_better"`
	Views         []string     `json:"views"`
	Person        string       `json:"person,omitempty"`
	Dims          []catalogDim `json:"dims"`
}

// widgetCatalog lists the measures with everything the builder needs to offer
// them: the views, the splits, and each filter's choices for this organization.
func (a *App) widgetCatalog(orgID uuid.UUID) []catalogMeasure {
	options := map[string][]dimOption{}
	optionsFor := func(d measureDim) []dimOption {
		key := string(d.Kind)
		if d.Kind == dimField {
			key += ":" + d.Field
		}
		if cached, ok := options[key]; ok {
			return cached
		}
		var out []dimOption
		switch d.Kind {
		case dimEnum, dimText:
			return nil
		case dimField:
			out = a.fieldOptions(orgID, d.Field)
		default:
			out = a.kindOptions(orgID, d.Kind)
		}
		options[key] = out
		return out
	}

	out := make([]catalogMeasure, 0, len(widgetMeasures))
	for i := range widgetMeasures {
		m := &widgetMeasures[i]
		entry := catalogMeasure{
			Key: m.Key, Area: m.Area, Unit: m.Unit, Snapshot: m.Snapshot(),
			LowerIsBetter: m.LowerIsBetter, Views: m.Views(), Person: m.Person,
			Dims: make([]catalogDim, 0, len(m.Dims)),
		}
		for _, d := range m.Dims {
			entry.Dims = append(entry.Dims, catalogDim{
				Key: d.Key, Kind: d.Kind, Values: d.Values, Field: d.Field,
				Split: true, Filter: d.Kind != dimText, Person: d.Kind == dimUser,
				Opts: optionsFor(d),
			})
		}
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool { return areaOrder[out[i].Area] < areaOrder[out[j].Area] })
	return out
}

var areaOrder = map[string]int{
	"inbox": 0, "contacts": 1, "deals": 2, "followups": 3,
	"automations": 4, "calls": 5, "messaging": 6, "chatbot": 7,
}

// GetWidgetCatalog lists the measures the widget builder offers.
func (a *App) GetWidgetCatalog(r *fastglue.Request) error {
	orgID, _, err := a.requireAuth(r, models.ResourceAnalytics, models.ActionRead)
	if err != nil {
		return nil
	}
	return r.SendEnvelope(map[string]any{"measures": a.widgetCatalog(orgID)})
}

// PreviewWidget computes a widget that has not been saved, so the builder can
// show it as it is being made — the only way to know a choice is the right
// one is to see it.
func (a *App) PreviewWidget(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceAnalytics, models.ActionRead)
	if err != nil {
		return nil
	}
	var req WidgetRequest
	if err := a.decodeRequest(r, &req); err != nil {
		return nil
	}
	if measureKey(req.Config) == "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Choose what the widget shows", nil, "")
	}
	if msg := normalizeMeasureWidget(&req); msg != "" {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, msg, nil, "")
	}
	widget := models.Widget{
		OrganizationID: orgID,
		Name:           req.Name,
		DataSource:     req.DataSource,
		Metric:         req.Metric,
		Filters:        filtersToJSONB(req.Filters),
		DisplayType:    req.DisplayType,
		ChartType:      req.ChartType,
		GroupByField:   req.GroupByField,
		Config:         models.JSONB(req.Config),
	}
	fromStr := string(r.RequestCtx.QueryArgs().Peek("from"))
	toStr := string(r.RequestCtx.QueryArgs().Peek("to"))
	data, err := a.executeWidgetQuery(orgID, userID, widget, fromStr, toStr)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to compute the preview", nil, "")
	}
	return r.SendEnvelope(data)
}
