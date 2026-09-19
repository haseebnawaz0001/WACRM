package models

// The dashboard an organization starts with.
//
// New organizations used to get the six widgets the product had before the
// CRM — message totals, campaigns, chatbot sessions — and nothing about the
// work the CRM exists to track: who is waiting for a reply, which follow-ups
// are late, what was won. The CRM widgets reached existing organizations
// through a one-off migration and never reached new ones at all.
//
// One list now, used for both: a new organization is seeded from it, and a
// migration adds whatever an existing organization is missing.

// WidgetViewDisplay maps a measure view to the display and chart type a widget
// row keeps.
var WidgetViewDisplay = map[string][2]string{
	"number":      {"number", ""},
	"trend":       {"chart", "line"},
	"bar":         {"chart", "bar"},
	"pie":         {"chart", "pie"},
	"table":       {"table", ""},
	"leaderboard": {"leaderboard", ""},
	"funnel":      {"funnel", ""},
}

// DefaultWidget is one widget a dashboard starts with.
type DefaultWidget struct {
	Name        string
	Description string

	// A widget built on a measure names the measure, how it is shown and,
	// for a split, what it is split by.
	Measure string
	View    string
	Split   string

	// A widget from the original builder names its source directly.
	DataSource  string
	DisplayType string
	Config      JSONB

	Filters  JSONBArray
	Color    string
	Snapshot bool // a count of right now: no change from a previous period
	X, Y     int
	W, H     int
}

// Widget builds the row for a default widget.
func (d DefaultWidget) Widget() Widget {
	w := Widget{
		Name:        d.Name,
		Description: d.Description,
		Metric:      "count",
		Filters:     d.Filters,
		Color:       d.Color,
		Size:        "small",
		GridX:       d.X,
		GridY:       d.Y,
		GridW:       d.W,
		GridH:       d.H,
		IsShared:    true,
		IsDefault:   true,
	}
	if w.Filters == nil {
		w.Filters = JSONBArray{}
	}
	if d.Measure != "" {
		display := WidgetViewDisplay[d.View]
		w.DataSource = "measure"
		w.DisplayType, w.ChartType = display[0], display[1]
		w.GroupByField = d.Split
		w.Config = JSONB{"measure": d.Measure, "view": d.View}
		if d.View == "funnel" {
			w.Config["funnel"] = map[string]string{"lifecycle_funnel": "lifecycle", "pipeline_funnel": "pipeline"}[d.Measure]
		}
		w.ShowChange = d.View == "number" && !d.Snapshot
		return w
	}
	w.DataSource = d.DataSource
	w.DisplayType = d.DisplayType
	if w.DisplayType == "" {
		w.DisplayType = "number"
	}
	w.Config = d.Config
	w.ShowChange = w.DisplayType == "number"
	return w
}

// MeasureDefaultWidgets are the widgets built on measures: what needs
// attention now, how the team is doing, and what the CRM is producing. Their
// positions are relative to where they are placed.
func MeasureDefaultWidgets() []DefaultWidget {
	return []DefaultWidget{
		{Name: "Waiting for a reply", Description: "Customers whose last message has no answer yet",
			Measure: "conversations_waiting", View: "number", Color: "blue", Snapshot: true, X: 0, Y: 0, W: 3, H: 3},
		{Name: "Unassigned conversations", Description: "Open conversations nobody has picked up",
			Measure: "conversations_unassigned", View: "number", Color: "blue", Snapshot: true, X: 3, Y: 0, W: 3, H: 3},
		{Name: "Overdue follow-ups", Description: "Follow-ups past their due time",
			Measure: "tasks_overdue", View: "number", Color: "orange", Snapshot: true, X: 6, Y: 0, W: 3, H: 3},
		{Name: "First reply time", Description: "How long customers waited for the first answer",
			Measure: "first_reply_time", View: "number", Color: "blue", X: 9, Y: 0, W: 3, H: 3},
		{Name: "New conversations", Description: "Conversations started each day",
			Measure: "conversations_started", View: "trend", Color: "blue", X: 0, Y: 3, W: 6, H: 8},
		{Name: "Resolved by agent", Description: "Who closed the most conversations",
			Measure: "conversations_resolved", View: "leaderboard", Split: "resolver", Color: "blue", X: 6, Y: 3, W: 6, H: 8},
		{Name: "Deals won", Description: "Value of the deals won in this period",
			Measure: "deals_won_value", View: "number", Color: "green", X: 0, Y: 11, W: 3, H: 3},
		{Name: "Automations that failed", Description: "Automation runs that could not finish",
			Measure: "automation_failures", View: "number", Color: "purple", X: 3, Y: 11, W: 3, H: 3},
		{Name: "New contacts by source", Description: "Where this period's new contacts came from",
			Measure: "contacts_new", View: "pie", Split: "source", Color: "green", X: 6, Y: 11, W: 6, H: 8},
		{Name: "Lifecycle funnel", Description: "How far contacts got in this period",
			Measure: "lifecycle_funnel", View: "funnel", Color: "green", X: 0, Y: 14, W: 6, H: 8},
	}
}

// DefaultDashboard is everything a new organization's dashboard starts with,
// laid out: what needs attention first, then the team and the pipeline, then
// the messaging totals the product has always shown.
func DefaultDashboard() []DefaultWidget {
	out := MeasureDefaultWidgets()
	below := func(y int, ws ...DefaultWidget) {
		for _, w := range ws {
			w.Y += y
			out = append(out, w)
		}
	}
	below(22,
		DefaultWidget{Name: "My open follow-ups", Description: "Follow-ups you owe",
			Measure: "tasks_open", View: "number", Color: "orange", Snapshot: true,
			Filters: JSONBArray{map[string]any{"field": "owner", "operator": "equals", "value": "me"}},
			X:       6, Y: 0, W: 3, H: 3},
		DefaultWidget{Name: "Pipeline value", Description: "Value of the open deals on the board",
			Measure: "deals_open_value", View: "number", Color: "green", Snapshot: true, X: 9, Y: 0, W: 3, H: 3},
		DefaultWidget{Name: "Deals by stage", Description: "Where the open deals are sitting",
			Measure: "deals_open", View: "bar", Split: "stage", Color: "green", X: 6, Y: 3, W: 6, H: 8},
		DefaultWidget{Name: "Open conversations", Description: "Open conversations by status",
			Measure: "conversations_open", View: "bar", Split: "status", Color: "blue", X: 0, Y: 0, W: 6, H: 8},
	)
	below(33,
		DefaultWidget{Name: "Total Messages", Description: "Total number of messages sent and received",
			DataSource: "messages", Color: "blue", X: 0, Y: 0, W: 3, H: 3},
		DefaultWidget{Name: "Active Contacts", Description: "Number of contacts with recent activity",
			DataSource: "contacts", Color: "green", X: 3, Y: 0, W: 3, H: 3},
		DefaultWidget{Name: "Chatbot Sessions", Description: "Active chatbot conversation sessions",
			DataSource: "sessions", Color: "purple", X: 6, Y: 0, W: 3, H: 3},
		DefaultWidget{Name: "Total Campaigns", Description: "Number of bulk message campaigns",
			DataSource: "campaigns", Color: "orange", X: 9, Y: 0, W: 3, H: 3},
		DefaultWidget{Name: "Recent Messages", Description: "Latest conversations from your contacts",
			DataSource: "messages", DisplayType: "table", X: 0, Y: 3, W: 6, H: 8},
		DefaultWidget{Name: "Quick Actions", Description: "Common tasks and shortcuts",
			DataSource: "shortcuts", DisplayType: "shortcuts",
			Config: JSONB{"shortcuts": []any{"chat", "campaigns", "templates", "chatbot"}}, X: 6, Y: 3, W: 6, H: 8},
	)
	return out
}
