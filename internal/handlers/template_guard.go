package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/notify"
)

// pausableCampaignStatuses are the campaigns a template change can still save.
//
// A processing campaign is already mid-send: pausing it there would strand half
// an audience, and Meta has accepted those sends regardless. A completed one is
// history.
var pausableCampaignStatuses = []models.CampaignStatus{
	models.CampaignStatusDraft,
	models.CampaignStatusScheduled,
	models.CampaignStatusQueued,
}

// guardTemplateDependents pauses campaigns that depend on a template which is
// no longer approved (plan 10, S8).
//
// Meta moves templates out of APPROVED on its own schedule — a quality drop, a
// policy flag, a pause. Nothing in the product noticed. A campaign scheduled for
// tomorrow morning would wake up, materialise its audience, and fail every
// single recipient against an unapproved template; the first anyone knew of it
// was a campaign report that was entirely red. Pausing it instead keeps the
// audience intact and tells the owner while there is still time to fix it.
func (a *App) GuardTemplateDependents(template *models.Template, reason string) int64 {
	if template == nil || template.Status == "APPROVED" {
		return 0
	}
	detail := fmt.Sprintf("Template %q is now %s", template.Name, template.Status)
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		detail += ": " + trimmed
	}
	return a.pauseDependentCampaigns(template, detail,
		"Resume it once the template is approved again.",
		map[string]any{"template_status": template.Status, "reason": reason})
}

// guardDeletedTemplateDependents pauses campaigns whose template has been
// deleted.
//
// Deleting left them pointing at a row that no longer existed. A draft stayed
// editable and looked fine until somebody started it; a scheduled one fired on
// time and failed every recipient. Neither said why.
func (a *App) GuardDeletedTemplateDependents(template *models.Template) int64 {
	if template == nil {
		return 0
	}
	return a.pauseDependentCampaigns(template,
		fmt.Sprintf("Template %q was deleted", template.Name),
		"Point the campaign at another template before starting it.",
		map[string]any{"template_deleted": true})
}

// pauseDependentCampaigns stops every campaign that can still be saved from
// sending against a template that no longer works, and tells each owner why.
func (a *App) pauseDependentCampaigns(template *models.Template, detail, advice string, data map[string]any) int64 {
	var campaigns []models.BulkMessageCampaign
	if err := a.DB.
		Where("organization_id = ? AND template_id = ? AND status IN ?",
			template.OrganizationID, template.ID, pausableCampaignStatuses).
		Find(&campaigns).Error; err != nil {
		a.Log.Error("Failed to find campaigns depending on template",
			"error", err, "template_id", template.ID)
		return 0
	}
	if len(campaigns) == 0 {
		return 0
	}

	ids := make([]uuid.UUID, 0, len(campaigns))
	for i := range campaigns {
		ids = append(ids, campaigns[i].ID)
	}

	// Re-check the status in the UPDATE so a campaign that started sending
	// between the read and the write is not yanked out from under the worker.
	res := a.DB.Model(&models.BulkMessageCampaign{}).
		Where("id IN ? AND status IN ?", ids, pausableCampaignStatuses).
		Update("status", models.CampaignStatusPaused)
	if res.Error != nil {
		a.Log.Error("Failed to pause campaigns for broken template",
			"error", res.Error, "template_id", template.ID)
		return 0
	}

	a.Log.Warn("Paused campaigns because their template can no longer be sent",
		"template", template.Name,
		"detail", detail,
		"campaigns", res.RowsAffected,
	)

	a.notifyCampaignOwners(campaigns, template, detail, advice, data)
	return res.RowsAffected
}

// notifyCampaignOwners tells each campaign's creator why their campaign stopped.
//
// A paused campaign with no explanation looks like the product lost it. The
// notification names the template and the reason, which is the only way the
// owner can act on it.
func (a *App) notifyCampaignOwners(campaigns []models.BulkMessageCampaign, template *models.Template, detail, advice string, data map[string]any) {
	svc := a.Notify()
	if svc == nil {
		return
	}

	ctx := context.Background()
	for i := range campaigns {
		campaign := &campaigns[i]
		if campaign.CreatedBy == uuid.Nil {
			continue
		}

		payload := map[string]any{
			"template_id":   template.ID.String(),
			"template_name": template.Name,
		}
		for key, value := range data {
			payload[key] = value
		}

		err := svc.Send(ctx, notify.Input{
			OrgID:   campaign.OrganizationID,
			UserIDs: []uuid.UUID{campaign.CreatedBy},
			Type:    models.NotificationCampaignPaused,
			Title:   fmt.Sprintf("Campaign %q was paused", campaign.Name),
			Body:    detail + ". " + advice,
			Link:    "/campaigns/" + campaign.ID.String(),
			Entity:  notify.Entity{Type: "campaign", ID: &campaign.ID},
			Data:    payload,
		})
		if err != nil {
			a.Log.Error("Failed to notify campaign owner of template pause",
				"error", err, "campaign_id", campaign.ID)
		}
	}
}
