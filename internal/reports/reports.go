// Package reports answers the questions a CRM is bought for (plan 09).
//
// The dashboard could already count messages, contacts and campaigns. It could
// not say where new contacts come from, how many leads become customers, who is
// behind on follow-ups, or how fast anyone actually replies — which are the
// questions that decide what a team does next week.
//
// Every report is a plain SQL aggregate rather than a stored rollup, because a
// rollup is wrong between the change and the next job tick, and these numbers
// are looked at precisely when somebody is deciding something.
package reports

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DefaultRangeDays is how far back a report looks when the caller says nothing.
const DefaultRangeDays = 30

// Interval is a time bucket.
type Interval string

const (
	Day   Interval = "day"
	Week  Interval = "week"
	Month Interval = "month"
)

// Valid reports whether the interval is one Postgres date_trunc understands
// and we are willing to bucket by. The value reaches SQL, so it is checked
// against this list rather than interpolated as given.
func (i Interval) Valid() bool {
	switch i {
	case Day, Week, Month:
		return true
	}
	return false
}

// Range is the period a report covers, in the organization's own timezone.
//
// "Last week" means the business's week. A report bucketed in UTC puts a
// Monday-morning conversation in Karachi into Sunday, and the weekly numbers
// stop matching what anyone remembers.
type Range struct {
	From     time.Time
	To       time.Time
	Interval Interval
	Location *time.Location
}

// NewRange builds a range, filling in the defaults.
func NewRange(from, to *time.Time, interval string, loc *time.Location) Range {
	if loc == nil {
		loc = time.UTC
	}

	r := Range{Interval: Interval(interval), Location: loc}
	if !r.Interval.Valid() {
		r.Interval = Day
	}

	now := time.Now().In(loc)
	r.To = now
	if to != nil {
		// An inclusive end date means the whole of that day, not midnight at
		// the start of it — otherwise "to today" silently excludes today.
		r.To = endOfDay(*to, loc)
	}
	r.From = r.To.AddDate(0, 0, -DefaultRangeDays)
	if from != nil {
		r.From = startOfDay(*from, loc)
	}
	return r
}

func startOfDay(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

// nowIn is the current moment in a location, extracted so "today" is a single
// idea rather than repeated clock reads that can straddle midnight.
func nowIn(loc *time.Location) time.Time { return time.Now().In(loc) }

func endOfDay(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 23, 59, 59, 999999999, loc)
}

// Days is how many days the range spans, for "per day" figures.
func (r Range) Days() float64 {
	days := r.To.Sub(r.From).Hours() / 24
	if days < 1 {
		return 1
	}
	return days
}

// bucket returns the SQL expression that groups a timestamp into this range's
// interval, in the organization's timezone.
//
// The interval is validated rather than interpolated blindly, and the timezone
// is a bind parameter, so neither can carry anything but a bucket.
func (r Range) bucket(column string) (string, any) {
	interval := string(r.Interval)
	if !r.Interval.Valid() {
		interval = string(Day)
	}
	return fmt.Sprintf("date_trunc('%s', %s AT TIME ZONE ?)", interval, column), r.Location.String()
}

// Service runs reports.
type Service struct {
	DB *gorm.DB
}

// New builds a Service.
func New(db *gorm.DB) *Service { return &Service{DB: db} }

// Viewer is who is asking, which decides whose rows they may see.
type Viewer struct {
	OrgID  uuid.UUID
	UserID uuid.UUID
	// SeesEveryone is true for roles allowed the whole team's numbers. An
	// agent sees only their own row, because a leaderboard everyone can read
	// is a performance review nobody agreed to.
	SeesEveryone bool
}

// Bucket is one point of a time series.
type Bucket struct {
	// Period is the start of the bucket, in the organization's timezone.
	Period time.Time `json:"period"`
	// Series is the dimension value, e.g. the contact source.
	Series string `json:"series"`
	Count  int64  `json:"count"`
}
