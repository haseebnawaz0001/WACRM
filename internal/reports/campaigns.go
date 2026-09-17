package reports

import (
	"context"
	"time"
)

// R6: did the campaign work? (plan 09.)
//
// "Delivered" and "read" are the numbers Meta gives back, and a campaign can
// score well on both while achieving nothing. A reply is the first thing a
// customer does that the business asked for, and it is what separates a list
// worth mailing from one that merely tolerates being mailed.
//
// Replies are attributed by the conversation the customer opened after a send,
// within the attribution window — so a reply three weeks later is not credited
// to a campaign nobody remembers receiving.

// CampaignReplyRow is one campaign's outcome.
type CampaignReplyRow struct {
	CampaignID string    `json:"campaign_id"`
	Name       string    `json:"name"`
	StartedAt  time.Time `json:"started_at"`

	// Recipients is what the campaign went out to.
	Recipients int64 `json:"recipients"`
	// Delivered is what Meta confirmed arriving.
	Delivered int64 `json:"delivered"`
	// Replied is how many of them wrote back inside the window.
	Replied int64 `json:"replied"`

	// ReplyRate is replies over **delivered**, not over recipients.
	//
	// A message that never arrived cannot be replied to, and counting it
	// against the campaign blames the copy for a delivery problem.
	ReplyRate float64 `json:"reply_rate"`
}

// CampaignReplies is the report.
type CampaignReplies struct {
	Rows []CampaignReplyRow `json:"rows"`
	// Totals across the period, computed the same way as a row.
	Recipients int64   `json:"recipients"`
	Delivered  int64   `json:"delivered"`
	Replied    int64   `json:"replied"`
	ReplyRate  float64 `json:"reply_rate"`
	// CountingRule is stated in the response, so two people reading the same
	// number cannot disagree about what it meant.
	CountingRule string `json:"counting_rule"`
}

// CampaignReplies reports reply rate per campaign started in the period.
func (s *Service) CampaignReplies(ctx context.Context, v Viewer, r Range) (*CampaignReplies, error) {
	type row struct {
		CampaignID string
		Name       string
		StartedAt  time.Time
		Recipients int64
		Delivered  int64
		Replied    int64
	}

	var rows []row
	err := s.DB.WithContext(ctx).Raw(`
		SELECT c.id::text            AS campaign_id,
		       c.name                AS name,
		       c.started_at          AS started_at,
		       c.total_recipients    AS recipients,
		       c.delivered_count     AS delivered,
		       c.replied_count       AS replied
		FROM bulk_message_campaigns c
		WHERE c.organization_id = ?
		  AND c.deleted_at IS NULL
		  AND c.started_at IS NOT NULL
		  AND c.started_at >= ? AND c.started_at <= ?
		ORDER BY c.started_at DESC`,
		v.OrgID, r.From, r.To).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	out := &CampaignReplies{
		Rows: make([]CampaignReplyRow, 0, len(rows)),
		CountingRule: "A reply is a conversation the contact opened within the attribution " +
			"window after receiving this campaign. The rate is replies over delivered, " +
			"because a message that never arrived cannot be replied to.",
	}

	for _, entry := range rows {
		out.Rows = append(out.Rows, CampaignReplyRow{
			CampaignID: entry.CampaignID,
			Name:       entry.Name,
			StartedAt:  entry.StartedAt,
			Recipients: entry.Recipients,
			Delivered:  entry.Delivered,
			Replied:    entry.Replied,
			ReplyRate:  rate(entry.Replied, entry.Delivered),
		})
		out.Recipients += entry.Recipients
		out.Delivered += entry.Delivered
		out.Replied += entry.Replied
	}
	out.ReplyRate = rate(out.Replied, out.Delivered)

	return out, nil
}

// rate is a percentage, or zero when there is nothing to divide by. A campaign
// that delivered nothing has no reply rate; reporting 100% or NaN would both
// be worse than reporting none.
func rate(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100
}
