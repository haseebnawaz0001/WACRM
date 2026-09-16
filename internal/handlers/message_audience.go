package handlers

import (
	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
)

// FilterUsersWhoCanSeeContact narrows a list of users to those allowed to see a
// contact's conversation (plan 10, S10).
//
// `new_message` was broadcast to the whole organization, payload included: the
// contact's name and the full text of the message. Every connected client
// received every customer message in the org, whatever the sender's permissions
// or the receiver's — a support agent who could not open a contact in the list
// still had that contact's messages arriving in their socket, and the same
// leak fed the toast notifications. The visibility rules already existed; the
// realtime layer simply was not consulting them.
//
// It takes the candidate list rather than reading the hub so the rule can be
// tested without a socket, and answers with three queries regardless of how
// many users are online: per-user checks would put a query per connected client
// on every inbound message.
func (a *App) FilterUsersWhoCanSeeContact(orgID uuid.UUID, contact *models.Contact, candidates []uuid.UUID) []uuid.UUID {
	if contact == nil || len(candidates) == 0 {
		return nil
	}

	allowed := map[uuid.UUID]bool{}
	if contact.AssignedUserID != nil {
		allowed[*contact.AssignedUserID] = true
	}

	var transferAgents []uuid.UUID
	a.DB.Model(&models.AgentTransfer{}).
		Where("organization_id = ? AND contact_id = ? AND status = ? AND agent_id IS NOT NULL",
			orgID, contact.ID, models.TransferStatusActive).
		Pluck("agent_id", &transferAgents)
	for _, id := range transferAgents {
		allowed[id] = true
	}

	// The live conversation decides queue visibility: unassigned and not held
	// by the bot means it is waiting for a human, and any agent who could take
	// it is entitled to see it arrive.
	var conv models.Conversation
	generalQueue := false
	if err := a.DB.
		Where("organization_id = ? AND contact_id = ? AND status <> ? AND deleted_at IS NULL",
			orgID, contact.ID, models.ConversationResolved).
		Order("opened_at DESC").First(&conv).Error; err == nil {
		if conv.AssigneeID != nil {
			allowed[*conv.AssigneeID] = true
		} else if !conv.BotActive {
			if conv.TeamID == nil {
				generalQueue = true
			} else {
				var teamMembers []uuid.UUID
				a.DB.Model(&models.TeamMember{}).
					Where("team_id = ? AND deleted_at IS NULL", *conv.TeamID).
					Pluck("user_id", &teamMembers)
				for _, id := range teamMembers {
					allowed[id] = true
				}
			}
		}
	}

	out := make([]uuid.UUID, 0, len(candidates))
	for _, userID := range candidates {
		switch {
		case allowed[userID], generalQueue:
			out = append(out, userID)
		case a.HasPermission(userID, models.ResourceContacts, models.ActionRead, orgID):
			// Whoever can open every contact can be told about every message.
			out = append(out, userID)
		}
	}
	return out
}
