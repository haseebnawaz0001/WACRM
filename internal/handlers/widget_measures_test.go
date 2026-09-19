package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/test/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMeasureTestApp(t *testing.T) *App {
	t.Helper()
	app := &App{DB: testutil.SetupTestDB(t), Log: testutil.NopLogger()}
	// The organization's timezone is read through the settings cache.
	app.Redis = testutil.SetupTestRedis(t)
	if app.Redis == nil {
		t.Skip("TEST_REDIS_URL not set, skipping test")
	}
	return app
}

// sampleValue is a value a filter on the dimension could hold.
func sampleValue(d measureDim) string {
	switch {
	case d.Kind == dimUser:
		return "me"
	case idKinds[d.Kind]:
		return uuid.NewString()
	case d.Kind == dimEnum:
		return d.Values[0]
	default:
		return "anything"
	}
}

// Every measure has to run against the real schema, in every view it offers,
// split and narrowed every way it offers. A measure is a hand-written query; a
// column name that is wrong would otherwise surface as a dashboard card that
// quietly reads zero.
func TestEveryMeasureRunsAgainstTheSchema(t *testing.T) {
	app := newMeasureTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	viewer := testutil.CreateTestUser(t, app.DB, org.ID)
	start, end := time.Now().AddDate(0, -1, 0), time.Now()

	for i := range widgetMeasures {
		m := &widgetMeasures[i]
		if m.Funnel != "" {
			continue
		}
		t.Run(m.Key, func(t *testing.T) {
			_, err := app.measureValue(org.ID, viewer.ID, m, nil, &start, &end)
			require.NoError(t, err, "value")

			if m.When != "" {
				_, err := app.measureTrend(org.ID, viewer.ID, m, nil, start, end)
				require.NoError(t, err, "trend")
			}

			for _, d := range m.Dims {
				d := d
				_, err := app.measureSplit(org.ID, viewer.ID, m, &d, nil, &start, &end, false)
				require.NoError(t, err, "split by %s", d.Key)
				if d.Kind == dimUser {
					_, err := app.measureSplit(org.ID, viewer.ID, m, &d, nil, &start, &end, true)
					require.NoError(t, err, "rank by %s", d.Key)
				}
				if d.Kind == dimText {
					continue
				}
				for _, op := range []string{"equals", "not_equals"} {
					filters := []FilterInput{{Field: d.Key, Operator: op, Value: sampleValue(d)}}
					_, err := app.measureValue(org.ID, viewer.ID, m, filters, &start, &end)
					require.NoError(t, err, "filter %s %s", d.Key, op)
				}
			}
		})
	}
}

// The catalog's shape is what the builder trusts: every leaderboard ranks a
// person, every split key is unique within its measure, every area has a
// colour and a place in the list.
func TestMeasureDefinitionsHangTogether(t *testing.T) {
	seen := map[string]bool{}
	for _, m := range widgetMeasures {
		assert.False(t, seen[m.Key], "%s is defined twice", m.Key)
		seen[m.Key] = true

		_, ordered := areaOrder[m.Area]
		assert.True(t, ordered, "%s has an area the builder does not list", m.Key)
		assert.NotEmpty(t, areaColor[m.Area], "%s has an area with no colour", m.Key)

		if m.Funnel != "" {
			assert.Equal(t, []string{"funnel"}, m.Views())
			continue
		}
		assert.NotEmpty(t, m.Table)
		assert.NotEmpty(t, m.Alias)

		keys := map[string]bool{}
		for _, d := range m.Dims {
			assert.False(t, keys[d.Key], "%s splits by %s twice", m.Key, d.Key)
			keys[d.Key] = true
			if d.Kind == dimEnum {
				assert.NotEmpty(t, d.Values, "%s.%s is an enum with no values", m.Key, d.Key)
			}
		}
		if m.Person != "" {
			d := (&m).Dim(m.Person)
			require.NotNil(t, d, "%s ranks by %s, which it cannot split by", m.Key, m.Person)
			assert.Equal(t, dimUser, d.Kind, "%s ranks something that is not a person", m.Key)
		}

		views := m.Views()
		assert.Equal(t, m.When != "", contains(views, "trend"), "%s: a trend needs a period", m.Key)
		assert.Equal(t, m.Person != "", contains(views, "leaderboard"), "%s: a leaderboard needs a person", m.Key)
	}
}

func TestNormalizeMeasureWidget(t *testing.T) {
	t.Run("an unknown measure is refused", func(t *testing.T) {
		req := WidgetRequest{Config: map[string]any{"measure": "nope"}}
		assert.NotEmpty(t, normalizeMeasureWidget(&req))
	})

	t.Run("a view the measure cannot take is refused", func(t *testing.T) {
		// A count of right now has no trend.
		req := WidgetRequest{Config: map[string]any{"measure": "tasks_overdue", "view": "trend"}}
		assert.NotEmpty(t, normalizeMeasureWidget(&req))
	})

	t.Run("a split view needs something to split by", func(t *testing.T) {
		req := WidgetRequest{Config: map[string]any{"measure": "deals_open", "view": "bar"}}
		assert.NotEmpty(t, normalizeMeasureWidget(&req))
		req = WidgetRequest{Config: map[string]any{"measure": "deals_open", "view": "bar"}, GroupByField: "stage"}
		assert.Empty(t, normalizeMeasureWidget(&req))
		assert.Equal(t, "chart", req.DisplayType)
		assert.Equal(t, "bar", req.ChartType)
		assert.Equal(t, "measure", req.DataSource)
	})

	t.Run("a leaderboard ranks the measure's person when none is chosen", func(t *testing.T) {
		req := WidgetRequest{Config: map[string]any{"measure": "conversations_resolved", "view": "leaderboard"}}
		assert.Empty(t, normalizeMeasureWidget(&req))
		assert.Equal(t, "resolver", req.GroupByField)
		assert.Equal(t, "leaderboard", req.DisplayType)
	})

	t.Run("filters must name something the measure can be narrowed by", func(t *testing.T) {
		for _, f := range []FilterInput{
			{Field: "owner_id", Operator: "equals", Value: "me"},  // a column, not a dimension
			{Field: "reason", Operator: "equals", Value: "price"}, // free text is split-only
			{Field: "owner", Operator: "contains", Value: "x"},    // only is / is not
			{Field: "owner", Operator: "equals", Value: "   "},    // no value
		} {
			req := WidgetRequest{Config: map[string]any{"measure": "deals_lost"}, Filters: []FilterInput{f}}
			assert.NotEmpty(t, normalizeMeasureWidget(&req), "%+v should be refused", f)
		}
	})

	t.Run("a count of right now shows no change from a previous period", func(t *testing.T) {
		req := WidgetRequest{Config: map[string]any{"measure": "conversations_waiting"}}
		assert.Empty(t, normalizeMeasureWidget(&req))
		assert.False(t, *req.ShowChange)

		req = WidgetRequest{Config: map[string]any{"measure": "deals_won"}}
		assert.Empty(t, normalizeMeasureWidget(&req))
		assert.True(t, *req.ShowChange)
		assert.Equal(t, "green", req.Color, "the colour comes from the area when none is chosen")
	})
}

// The numbers a person reads off the dashboard have to be the right ones.
func TestMeasureValues(t *testing.T) {
	app := newMeasureTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	me := testutil.CreateTestUser(t, app.DB, org.ID)
	other := testutil.CreateTestUser(t, app.DB, org.ID)
	contact := testutil.CreateTestContact(t, app.DB, org.ID)
	now := time.Now()
	start, end := now.AddDate(0, 0, -7), now
	ago := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }

	// Conversations: one waiting on me, one unassigned and waiting, one resolved.
	// Each on its own contact: a contact has one conversation open at a time.
	conversation := func(status models.ConversationStatus, assignee *uuid.UUID, waiting *time.Time, opened time.Time) *models.Conversation {
		c := &models.Conversation{
			OrganizationID: org.ID, ContactID: testutil.CreateTestContact(t, app.DB, org.ID).ID, Status: status, AssigneeID: assignee,
			Handling: models.ConversationHandling("agent"), WhatsAppAccount: "main", OpenedAt: opened, WaitingSince: waiting,
		}
		require.NoError(t, app.DB.Create(c).Error)
		return c
	}
	conversation(models.ConversationOpen, &me.ID, ago(time.Hour), now.Add(-2*time.Hour))
	conversation(models.ConversationOpen, nil, ago(time.Hour), now.Add(-3*time.Hour))
	resolved := conversation(models.ConversationResolved, &other.ID, nil, now.Add(-5*time.Hour))
	require.NoError(t, app.DB.Model(resolved).Updates(map[string]any{
		"resolved_at": now.Add(-time.Hour), "resolved_by_id": other.ID,
		"first_customer_message_at": now.Add(-5 * time.Hour), "first_response_at": now.Add(-4 * time.Hour),
	}).Error)

	value := func(key string, filters ...FilterInput) float64 {
		t.Helper()
		v, err := app.measureValue(org.ID, me.ID, measuresByKey[key], filters, &start, &end)
		require.NoError(t, err)
		return v
	}

	assert.Equal(t, 2.0, value("conversations_waiting"))
	assert.Equal(t, 1.0, value("conversations_waiting", FilterInput{Field: "assignee", Operator: "equals", Value: "me"}),
		"\"me\" means the person looking")
	assert.Equal(t, 1.0, value("conversations_waiting", FilterInput{Field: "assignee", Operator: "not_equals", Value: "me"}),
		"\"is not me\" still counts the unassigned conversation")
	assert.Equal(t, 1.0, value("conversations_unassigned"))
	assert.Equal(t, 1.0, value("conversations_resolved"))
	assert.InDelta(t, 60.0, value("first_reply_time"), 0.5, "first reply time is in minutes")

	// A count of right now ignores the date range.
	old, oldEnd := now.AddDate(-1, 0, 0), now.AddDate(-1, 0, 1)
	waiting, err := app.measureValue(org.ID, me.ID, measuresByKey["conversations_waiting"], nil, &old, &oldEnd)
	require.NoError(t, err)
	assert.Equal(t, 2.0, waiting)

	// Follow-ups: one overdue, one due tomorrow.
	taskType := &models.TaskType{OrganizationID: org.ID, Key: "call_back", Label: "Call back", Color: "blue"}
	require.NoError(t, app.DB.Create(taskType).Error)
	task := func(due time.Time) {
		require.NoError(t, app.DB.Create(&models.Task{
			OrganizationID: org.ID, ContactID: contact.ID, TypeID: taskType.ID, Title: "Call",
			OwnerID: me.ID, Priority: models.TaskPriorityNormal, Status: models.TaskOpen, DueAt: due,
			Source: models.TaskSourceManual,
		}).Error)
	}
	task(now.Add(-24 * time.Hour))
	task(now.Add(24 * time.Hour))
	assert.Equal(t, 1.0, value("tasks_overdue"))
	assert.Equal(t, 2.0, value("tasks_open"))

	// Deals: the value won is a sum, split by owner with names.
	pipeline := &models.Pipeline{OrganizationID: org.ID, Name: "Sales", Currency: "EUR", IsDefault: true}
	require.NoError(t, app.DB.Create(pipeline).Error)
	stage := &models.PipelineStage{OrganizationID: org.ID, PipelineID: pipeline.ID, Name: "Won", Position: 1}
	require.NoError(t, app.DB.Create(stage).Error)
	deal := func(status string, value float64, owner uuid.UUID) {
		closed := now.Add(-time.Hour)
		require.NoError(t, app.DB.Create(&models.Deal{
			OrganizationID: org.ID, PipelineID: pipeline.ID, StageID: stage.ID, ContactID: contact.ID,
			Title: "Deal", Value: value, Currency: "EUR", OwnerID: &owner, Status: status,
			StageEnteredAt: now, BoardPosition: "a", ClosedAt: &closed,
		}).Error)
	}
	deal(models.DealWon, 100, me.ID)
	deal(models.DealWon, 250, other.ID)
	deal(models.DealLost, 999, other.ID)
	assert.Equal(t, 350.0, value("deals_won_value"))
	assert.Equal(t, "EUR", app.measureCurrency(org.ID), "money is counted in the board's currency")

	m := measuresByKey["deals_won_value"]
	points, err := app.measureSplit(org.ID, me.ID, m, m.Dim("owner"), nil, &start, &end, true)
	require.NoError(t, err)
	require.Len(t, points, 2)
	assert.Equal(t, other.FullName, points[0].Label, "a leaderboard names people and ranks them")
	assert.Equal(t, 250.0, points[0].Value)
	assert.Equal(t, other.ID.String(), points[0].Key)
}

// A trend covers every day of the period, with quiet days as zero, so the line
// does not jump across gaps.
func TestMeasureTrendFillsQuietDays(t *testing.T) {
	app := newMeasureTestApp(t)
	org := testutil.CreateTestOrganization(t, app.DB)
	viewer := testutil.CreateTestUser(t, app.DB, org.ID)
	end := time.Now()
	start := end.AddDate(0, 0, -6)

	points, err := app.measureTrend(org.ID, viewer.ID, measuresByKey["contacts_new"], nil, start, end)
	require.NoError(t, err)
	assert.Len(t, points, 7)

	assert.Equal(t, "day", trendBucket(start, end))
	assert.Equal(t, "week", trendBucket(end.AddDate(0, -6, 0), end))
	assert.Equal(t, "month", trendBucket(end.AddDate(-2, 0, 0), end))
}

func TestMeasureChange(t *testing.T) {
	assert.Equal(t, 0.0, measureChange(0, 0))
	assert.Equal(t, 100.0, measureChange(0, 5))
	assert.Equal(t, -50.0, measureChange(10, 5))
	assert.InDelta(t, 12.5, measureChange(8, 9), 0.001)
}

// Every default widget has to be one the builder could have made: a measure
// that exists, in a view it offers, split by something it can be split by.
// Otherwise a new organization's dashboard opens with cards that read nothing.
func TestDefaultWidgetsAreValidMeasures(t *testing.T) {
	for _, spec := range models.DefaultDashboard() {
		if spec.Measure == "" {
			continue
		}
		built := spec.Widget()
		filters := []FilterInput{}
		for _, raw := range built.Filters {
			f := raw.(map[string]any)
			filters = append(filters, FilterInput{Field: f["field"].(string), Operator: f["operator"].(string), Value: f["value"].(string)})
		}
		req := WidgetRequest{
			Config:       map[string]any{"measure": spec.Measure, "view": spec.View},
			GroupByField: spec.Split,
			Filters:      filters,
		}
		require.Empty(t, normalizeMeasureWidget(&req), spec.Name)
		assert.Equal(t, req.DisplayType, built.DisplayType, spec.Name)
		assert.Equal(t, req.ChartType, built.ChartType, spec.Name)
		assert.Equal(t, req.GroupByField, built.GroupByField, spec.Name)
		assert.Equal(t, *req.ShowChange, built.ShowChange, "%s: snapshot flag disagrees with the measure", spec.Name)
	}
}

// Every measure, split and area the catalog offers has words in the interface.
// A measure added here without them would appear in the builder as its key —
// "conversations_waiting" — which is the thing the builder exists to hide.
func TestMeasuresHaveInterfaceCopy(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "i18n", "locales", "en.json"))
	if err != nil {
		t.Skip("frontend locale not found:", err)
	}
	var locale struct {
		Dashboard struct {
			Measures map[string]struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"measures"`
			Dims     map[string]string `json:"dims"`
			DimNouns map[string]string `json:"dimNouns"`
			Areas    map[string]string `json:"areas"`
		} `json:"dashboard"`
	}
	require.NoError(t, json.Unmarshal(raw, &locale))
	words := locale.Dashboard

	for _, m := range widgetMeasures {
		entry, ok := words.Measures[m.Key]
		assert.True(t, ok && entry.Name != "" && entry.Description != "", "%s has no name or description in en.json", m.Key)
		assert.NotEmpty(t, words.Areas[m.Area], "area %s has no name in en.json", m.Area)
		for _, d := range m.Dims {
			assert.NotEmpty(t, words.Dims[d.Key], "%s splits by %s, which has no name in en.json", m.Key, d.Key)
			assert.NotEmpty(t, words.DimNouns[d.Key], "%s splits by %s, which has no noun for widget names in en.json", m.Key, d.Key)
		}
	}
}
