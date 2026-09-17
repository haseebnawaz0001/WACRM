package calling

import (
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmevents"
	"github.com/shridarpatil/whatomate/internal/models"
)

// Call outcomes as CRM events (plan 10, S3 and §4.4).
//
// The calling manager has no *App — it is constructed with a database handle
// and a WebSocket hub — so it could not reach the webhook dispatcher and
// nothing outside the call screen ever learned that a call happened. A missed
// call is the canonical reason to want a callback task created automatically,
// and until now the only record of one was a row nobody was watching.
//
// Publishing is an outbox insert, which needs nothing but the DB. The relay
// does the fan-out.

// publishCallOutcome records how a call ended.
//
// Failures are logged, never propagated: the call is over, and the cleanup
// path that calls this has nothing useful to do with an error.
func (m *Manager) publishCallOutcome(callLogID uuid.UUID) {
	if m.db == nil || callLogID == uuid.Nil {
		return
	}

	var log models.CallLog
	if err := m.db.Where("id = ?", callLogID).First(&log).Error; err != nil {
		m.log.Error("Failed to load call log for event", "error", err, "call_log_id", callLogID)
		return
	}

	eventType := callEventType(log)
	if eventType == "" {
		return
	}

	event := crmevents.New(log.OrganizationID, eventType, crmevents.SystemActor(), map[string]any{
		"call_log_id":      log.ID.String(),
		"direction":        string(log.Direction),
		"status":           string(log.Status),
		"duration":         log.Duration,
		"whatsapp_account": log.WhatsAppAccount,
	})
	if log.ContactID != uuid.Nil {
		event = event.ForContact(log.ContactID)
	}
	if log.AgentID != nil {
		event.Data["agent_id"] = log.AgentID.String()
	}

	if err := crmevents.Publish(m.db, event); err != nil {
		m.log.Error("Failed to publish call event", "error", err, "call_log_id", callLogID)
	}
}

// callEventType decides which event a finished call is.
//
// Answered means somebody spoke to the customer; anything else that has
// stopped ringing means nobody did, which is what a "missed call" rule cares
// about. A call still in progress produces no event — there is no outcome yet.
func callEventType(log models.CallLog) string {
	switch log.Status {
	case models.CallStatusCompleted:
		if log.AnsweredAt != nil {
			return "call.completed"
		}
		// Completed without ever being answered is a missed call wearing the
		// wrong label: the session ended, nobody picked up.
		return "call.missed"
	case models.CallStatusMissed, models.CallStatusRejected, models.CallStatusFailed:
		return "call.missed"
	default:
		return ""
	}
}

// publishTransferNoAnswer records a call transfer nobody accepted.
func (m *Manager) publishTransferNoAnswer(orgID, callLogID, contactID uuid.UUID, teamID *uuid.UUID) {
	if m.db == nil {
		return
	}

	event := crmevents.New(orgID, "call.transfer_no_answer", crmevents.SystemActor(), map[string]any{
		"call_log_id": callLogID.String(),
		"at":          time.Now().UTC().Format(time.RFC3339),
	})
	if contactID != uuid.Nil {
		event = event.ForContact(contactID)
	}
	if teamID != nil {
		event.Data["team_id"] = teamID.String()
	}

	if err := crmevents.Publish(m.db, event); err != nil {
		m.log.Error("Failed to publish transfer no-answer event", "error", err)
	}
}
