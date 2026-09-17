package conversation

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
)

// Campaign reply attribution (plan 10, §4.5).
//
// A campaign could report how many messages went out and nothing about what
// came back. "Did that blast work?" is the only question anybody asks about a
// campaign, and the product could not answer it: a reply arrived as an
// ordinary inbound message with nothing tying it to the send.
//
// The link is made when the conversation opens, not when the campaign sends,
// because a send that gets no reply should attribute nothing.

// DefaultAttributionWindow is how long after a campaign send a reply still
// counts as a reply to it.
//
// 72 hours: long enough for someone who reads the message the next morning and
// answers after the weekend, short enough that a reply three weeks later is
// not credited to a campaign nobody remembers.
const DefaultAttributionWindow = 72 * time.Hour

// AttributeToCampaign links a newly opened conversation to the campaign that
// most recently messaged this contact, if one did so inside the window.
//
// Returns the campaign id when the link was made, so the caller can emit
// campaign.replied. A conversation that already has an origin keeps it: the
// first campaign to reach someone is the one that started the conversation.
func (s *Service) AttributeToCampaign(ctx context.Context, conv *models.Conversation, window time.Duration) (*uuid.UUID, error) {
	if conv == nil || conv.OriginCampaignID != nil {
		return nil, nil
	}
	if window <= 0 {
		window = DefaultAttributionWindow
	}

	var recipient models.BulkMessageRecipient
	err := s.DB.WithContext(ctx).
		Where("contact_id = ? AND sent_at IS NOT NULL AND sent_at > ?",
			conv.ContactID, conv.OpenedAt.Add(-window)).
		// The send that preceded the reply, not the newest send overall: a
		// campaign that went out after the customer wrote did not prompt it.
		Where("sent_at <= ?", conv.OpenedAt).
		Order("sent_at DESC").
		First(&recipient).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}

	campaignID := recipient.CampaignID
	if err := s.DB.WithContext(ctx).Model(&models.Conversation{}).
		// Re-check the column so two inbound messages arriving together
		// cannot both claim the attribution and emit two replied events.
		Where("id = ? AND origin_campaign_id IS NULL", conv.ID).
		Update("origin_campaign_id", campaignID).Error; err != nil {
		return nil, err
	}

	conv.OriginCampaignID = &campaignID
	return &campaignID, nil
}

// PublishCampaignReplied announces that a campaign got an answer.
func (s *Service) PublishCampaignReplied(tx *gorm.DB, conv *models.Conversation, campaignID uuid.UUID) error {
	event := crmevents.New(conv.OrganizationID, "campaign.replied",
		crmevents.SystemActor(), map[string]any{
			"campaign_id":     campaignID.String(),
			"conversation_id": conv.ID.String(),
		}).ForContact(conv.ContactID)
	return crmevents.PublishTx(tx, event)
}
