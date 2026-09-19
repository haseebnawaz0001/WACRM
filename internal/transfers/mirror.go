package transfers

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MirrorToConversation makes the contact's live conversation show what a
// transfer says (plan 03, §4.4: while a transfer is active, the transfer is the
// source of truth and the conversation mirrors it).
//
// The inbox lists conversations by assignee, and transfers used to change only
// who the transfer belonged to. An agent who picked a customer up from the
// queue therefore did not find them under Mine, and the queue kept showing a
// conversation somebody was already answering.
//
//   - An active transfer copies its agent (nil when queued) and its team.
//     An agent on it means a person is handling the conversation.
//   - A transfer that has ended clears the assignee, but only when it is still
//     the transfer's agent: somebody may have been assigned since, and the end
//     of an old handoff must not undo that.
//
// A contact with no live conversation is not an error; there is nothing to
// mirror onto.
func MirrorToConversation(tx *gorm.DB, t *models.AgentTransfer) error {
	live := tx.Model(&models.Conversation{}).
		Where("organization_id = ? AND contact_id = ? AND status <> ?",
			t.OrganizationID, t.ContactID, models.ConversationResolved)

	if t.Status != models.TransferStatusActive {
		if t.AgentID == nil {
			return nil
		}
		return live.Where("assignee_id = ?", *t.AgentID).
			Update("assignee_id", nil).Error
	}

	// The team is copied even when it is none: a transfer moved back to the
	// general queue must take the conversation with it, or the queue's team
	// filter keeps hiding it from everyone outside the old team.
	updates := map[string]any{"assignee_id": t.AgentID, "team_id": t.TeamID}
	if t.AgentID != nil {
		updates["handling"] = models.HandlingHuman
	}
	return live.Updates(updates).Error
}

// AssignActiveTx moves the contact's active transfer to whoever the
// conversation is being assigned to. It is the other direction of the mirror,
// and a contact with no active transfer is left alone.
//
// The inbox assigns conversations. If that left the transfer untouched, the
// transfer would still sit unpicked in the queue, its response deadline would
// still escalate, and the SLA view would report as waiting a customer an agent
// was already talking to.
//
// A nil agent returns the transfer to its queue; a nil team leaves the team
// alone. The caller must hold the contact lock — the conversation service does
// — so the canonical order (contact → conversation → transfer) holds.
func AssignActiveTx(tx *gorm.DB, orgID, contactID uuid.UUID, agentID, teamID *uuid.UUID) error {
	var transfer models.AgentTransfer
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("organization_id = ? AND contact_id = ? AND status = ?",
			orgID, contactID, models.TransferStatusActive).
		First(&transfer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	transfer.AgentID = agentID
	if teamID != nil {
		transfer.TeamID = teamID
	}
	switch {
	case agentID == nil:
		transfer.SLA.PickedUpAt = nil
	case transfer.SLA.PickedUpAt == nil:
		now := time.Now().UTC()
		transfer.SLA.PickedUpAt = &now
		if transfer.SLA.ResponseDeadline != nil && now.After(*transfer.SLA.ResponseDeadline) {
			transfer.SLA.Breached = true
			transfer.SLA.BreachedAt = &now
		}
	}
	return tx.Save(&transfer).Error
}
