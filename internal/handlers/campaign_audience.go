package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/phoneutil"
	"github.com/shridarpatil/whatomate/internal/templateutil"
	"github.com/valyala/fasthttp"
	"github.com/zerodha/fastglue"
	"gorm.io/gorm"
)

// Audience types.
const (
	AudienceList    = "list"
	AudienceSegment = "segment"
)

// Why a contact in the segment did not become a recipient.
const (
	ExcludedOptedOut     = "opted_out"
	ExcludedInvalidPhone = "invalid_phone"
	ExcludedDuplicate    = "duplicate"
)

// AudiencePreview is what a segment-targeted campaign would send to.
type AudiencePreview struct {
	Count int `json:"count"`
	// Excluded says who is being left out and why, because "12,400 contacts"
	// and "12,400 minus 3,000 who opted out" are different decisions.
	Excluded map[string]int `json:"excluded"`
	Sample   []SampleRow    `json:"sample"`
}

// SampleRow is one rendered example, so somebody can read what will actually
// arrive rather than approving a count.
type SampleRow struct {
	ContactID   string `json:"contact_id"`
	Name        string `json:"name"`
	PhoneNumber string `json:"phone_number"`
	Preview     string `json:"preview"`
}

// SampleSize is how many examples a preview renders.
const SampleSize = 10

// SetCampaignAudience points a draft campaign at a segment.
func (a *App) SetCampaignAudience(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceCampaigns, models.ActionWrite)
	if err != nil {
		return err
	}
	if !a.HasPermission(userID, models.ResourceSegments, models.ActionRead, orgID) {
		return r.SendErrorEnvelope(fasthttp.StatusForbidden, "Insufficient permissions", nil, "")
	}

	campaignID, err := parsePathUUID(r, "id", "campaign")
	if err != nil {
		return nil
	}
	campaign, err := findByIDAndOrg[models.BulkMessageCampaign](a.DB, r, campaignID, orgID, "Campaign")
	if err != nil {
		return nil
	}

	// Changing who a campaign is aimed at after it has started would mean the
	// recipient list no longer matches what was sent.
	if campaign.Status != models.CampaignStatusDraft && campaign.Status != models.CampaignStatusScheduled {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest,
			"The audience can only be changed while a campaign is a draft", nil, "")
	}

	var req struct {
		AudienceType string `json:"audience_type"`
		SegmentID    string `json:"segment_id"`
	}
	if err := json.Unmarshal(r.RequestCtx.PostBody(), &req); err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid request body", nil, "")
	}

	updates := map[string]any{"audience_type": AudienceList, "segment_id": nil}
	if req.AudienceType == AudienceSegment {
		segmentID, parseErr := uuid.Parse(req.SegmentID)
		if parseErr != nil {
			return r.SendErrorEnvelope(fasthttp.StatusBadRequest, "Invalid segment id", nil, "")
		}
		if _, getErr := a.Segments().Get(context.Background(), orgID, segmentID); getErr != nil {
			return r.SendErrorEnvelope(fasthttp.StatusNotFound, "Segment not found", nil, "")
		}
		updates["audience_type"] = AudienceSegment
		updates["segment_id"] = segmentID
	}

	if err := a.DB.Model(&models.BulkMessageCampaign{}).Where("id = ?", campaign.ID).
		Updates(updates).Error; err != nil {
		a.Log.Error("Failed to set campaign audience", "error", err, "campaign_id", campaign.ID)
		return r.SendErrorEnvelope(fasthttp.StatusInternalServerError, "Failed to set the audience", nil, "")
	}

	a.logAudit(orgID, userID, models.ResourceCampaigns, campaign.ID, models.AuditActionUpdated, nil,
		map[string]any{"audience_type": updates["audience_type"], "segment_id": updates["segment_id"]})

	reloaded, err := findByIDAndOrg[models.BulkMessageCampaign](a.DB, r, campaignID, orgID, "Campaign")
	if err != nil {
		return nil
	}
	return r.SendEnvelope(map[string]any{"campaign": reloaded})
}

// PreviewCampaignAudience shows who a segment-targeted campaign would reach.
func (a *App) PreviewCampaignAudience(r *fastglue.Request) error {
	orgID, userID, err := a.requireAuth(r, models.ResourceCampaigns, models.ActionRead)
	if err != nil {
		return err
	}
	campaignID, err := parsePathUUID(r, "id", "campaign")
	if err != nil {
		return nil
	}
	campaign, err := findByIDAndOrg[models.BulkMessageCampaign](a.DB, r, campaignID, orgID, "Campaign")
	if err != nil {
		return nil
	}

	preview, err := a.buildAudiencePreview(orgID, userID, campaign)
	if err != nil {
		return r.SendErrorEnvelope(fasthttp.StatusBadRequest, err.Error(), nil, "")
	}
	return r.SendEnvelope(preview)
}

func (a *App) buildAudiencePreview(orgID, userID uuid.UUID, campaign *models.BulkMessageCampaign) (*AudiencePreview, error) {
	if campaign.AudienceType != AudienceSegment || campaign.SegmentID == nil {
		return nil, fmt.Errorf("this campaign is not aimed at a segment")
	}

	contacts, excluded, err := a.segmentAudience(orgID, userID, campaign)
	if err != nil {
		return nil, err
	}

	preview := &AudiencePreview{Count: len(contacts), Excluded: excluded}

	var template models.Template
	hasTemplate := a.DB.Where("id = ? AND organization_id = ?", campaign.TemplateID, orgID).
		First(&template).Error == nil

	for i, contact := range contacts {
		if i >= SampleSize {
			break
		}
		row := SampleRow{
			ContactID:   contact.ID.String(),
			Name:        contact.ProfileName,
			PhoneNumber: contact.PhoneNumber,
		}
		if hasTemplate {
			// The rendered body, not the raw template: approving "Hi {{1}},
			// your order is ready" tells nobody what the customer will read.
			row.Preview = templateutil.ReplaceWithStringParams(template.BodyContent,
				map[string]string{"1": contact.ProfileName, "name": contact.ProfileName})
		}
		preview.Sample = append(preview.Sample, row)
	}
	return preview, nil
}

// segmentAudience resolves a campaign's segment into the contacts it would
// message, and counts the ones it would not.
//
// The scope is deliberately the whole organization rather than the caller's
// own contacts: a campaign is an organization-level action governed by the
// campaign permission, and an audience that silently shrank to whatever the
// person pressing Send happens to own would be a different campaign each time.
func (a *App) segmentAudience(orgID, userID uuid.UUID, campaign *models.BulkMessageCampaign) ([]models.Contact, map[string]int, error) {
	registry, err := a.contactRegistry(orgID)
	if err != nil {
		return nil, nil, err
	}

	viewer := contactquery.Viewer{
		OrgID:             orgID,
		UserID:            userID,
		Location:          a.OrgLocation(orgID),
		CanSeeAllContacts: true,
	}

	query, err := a.Segments().Apply(context.Background(), a.DB.Model(&models.Contact{}),
		registry, viewer, *campaign.SegmentID)
	if err != nil {
		return nil, nil, err
	}

	var contacts []models.Contact
	if err := query.Find(&contacts).Error; err != nil {
		return nil, nil, err
	}

	// Marketing templates must not reach anybody who has opted out, however
	// they got into the segment.
	var template models.Template
	marketing := a.DB.Where("id = ? AND organization_id = ?", campaign.TemplateID, orgID).
		First(&template).Error == nil && strings.EqualFold(template.Category, "MARKETING")

	excluded := map[string]int{}
	seen := map[string]bool{}
	kept := make([]models.Contact, 0, len(contacts))

	for _, contact := range contacts {
		if marketing && contact.MarketingOptOut {
			excluded[ExcludedOptedOut]++
			continue
		}

		normalised := phoneutil.Normalize(contact.PhoneNumber)
		if normalised == "" {
			excluded[ExcludedInvalidPhone]++
			continue
		}
		// Two contacts can share a number through bad data; messaging the same
		// person twice in one campaign is the visible failure.
		if seen[normalised] {
			excluded[ExcludedDuplicate]++
			continue
		}
		seen[normalised] = true
		kept = append(kept, contact)
	}

	return kept, excluded, nil
}

// materializeSegmentAudience turns a campaign's segment into recipient rows.
//
// It runs at start rather than at save, so the audience is who matched when the
// campaign went out. Recipients are written once: re-materialising a campaign
// that already has them would double-send.
func (a *App) materializeSegmentAudience(orgID, userID uuid.UUID, campaign *models.BulkMessageCampaign) (int, error) {
	if campaign.AudienceType != AudienceSegment || campaign.SegmentID == nil {
		return 0, nil
	}
	if campaign.MaterializedAt != nil {
		return 0, nil
	}

	contacts, excluded, err := a.segmentAudience(orgID, userID, campaign)
	if err != nil {
		return 0, err
	}

	segment, err := a.Segments().Get(context.Background(), orgID, *campaign.SegmentID)
	if err != nil {
		return 0, err
	}

	excludedTotal := 0
	for _, count := range excluded {
		excludedTotal += count
	}

	err = a.DB.Transaction(func(tx *gorm.DB) error {
		if len(contacts) > 0 {
			rows := make([]models.BulkMessageRecipient, 0, len(contacts))
			for _, contact := range contacts {
				id := contact.ID
				rows = append(rows, models.BulkMessageRecipient{
					BaseModel:     models.BaseModel{ID: uuid.New()},
					CampaignID:    campaign.ID,
					ContactID:     &id,
					PhoneNumber:   contact.PhoneNumber,
					RecipientName: contact.ProfileName,
					Status:        models.MessageStatusPending,
				})
			}
			if err := tx.CreateInBatches(rows, 500).Error; err != nil {
				return err
			}
		}

		now := time.Now().UTC()
		return tx.Model(&models.BulkMessageCampaign{}).Where("id = ?", campaign.ID).
			Updates(map[string]any{
				// The filter as it stood, so "who did this go to and why" is
				// answerable later even if the segment has since been edited.
				"audience_filter":  segment.Filter,
				"audience_count":   len(contacts),
				"excluded_count":   excludedTotal,
				"materialized_at":  now,
				"total_recipients": gorm.Expr("total_recipients + ?", len(contacts)),
			}).Error
	})
	if err != nil {
		return 0, err
	}

	a.Segments().MarkUsed(context.Background(), orgID, *campaign.SegmentID)
	return len(contacts), nil
}
