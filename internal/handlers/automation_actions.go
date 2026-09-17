package handlers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/crmactions"
	"github.com/shridarpatil/whatomate/internal/messaging"
	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/shridarpatil/whatomate/internal/templateutil"
	"github.com/shridarpatil/whatomate/internal/transfers"
)

// The actions that reach a customer live here rather than in crmactions,
// because sending and transferring need the App's account cache, chatbot
// settings and assigner. crmactions declares the interfaces; this file is the
// server's implementation of them.

// ServiceWindow is how long after a customer's last message WhatsApp allows a
// free-form reply. Outside it, only an approved template may be sent.
//
// Re-exported from internal/messaging, which owns the rule now (plan 00, F10):
// four send paths each had their own copy, and they disagreed.
const ServiceWindow = messaging.ServiceWindow

// automationMessenger sends on behalf of automation rules.
type automationMessenger struct{ app *App }

// SendText sends a free-form message, which WhatsApp only allows inside the
// service window.
func (m automationMessenger) SendText(ctx context.Context, orgID, contactID uuid.UUID, text string) (uuid.UUID, error) {
	contact, account, err := m.app.automationTarget(orgID, contactID)
	if err != nil {
		return uuid.Nil, err
	}

	// The window is checked here rather than left to Meta, so the run log says
	// "outside the 24-hour window" instead of relaying an opaque API error.
	if err := messaging.CheckFreeText(contact, time.Now().UTC()); err != nil {
		return uuid.Nil, err
	}

	message, err := m.app.SendOutgoingMessage(ctx, OutgoingMessageRequest{
		Account: account,
		Contact: contact,
		Type:    models.MessageTypeText,
		Content: text,
	}, automationSendOptions())
	if err != nil {
		return uuid.Nil, err
	}
	return message.ID, nil
}

// SendTemplate sends an approved template, which works outside the window.
func (m automationMessenger) SendTemplate(ctx context.Context, orgID, contactID, templateID uuid.UUID, params map[string]any) (uuid.UUID, error) {
	contact, account, err := m.app.automationTarget(orgID, contactID)
	if err != nil {
		return uuid.Nil, err
	}

	var template models.Template
	if err := m.app.DB.Where("id = ? AND organization_id = ?", templateID, orgID).
		First(&template).Error; err != nil {
		return uuid.Nil, fmt.Errorf("that template no longer exists")
	}
	// Approval and marketing consent are the same rules every other send path
	// applies (plan 00, F10). Consent in particular is about the message, not
	// the mechanism that sent it.
	if err := messaging.CheckTemplate(&template, contact, nil); err != nil {
		return uuid.Nil, err
	}

	// The template's own account wins over the contact's: a template is
	// approved against one WABA and cannot be sent from another.
	if template.WhatsAppAccount != "" {
		resolved, resolveErr := m.app.resolveWhatsAppAccount(orgID, template.WhatsAppAccount)
		if resolveErr != nil {
			return uuid.Nil, resolveErr
		}
		account = resolved
	}

	// A template with an unfilled placeholder is rejected by Meta, which the
	// run log would report as an opaque API error. Naming the parameter here
	// tells the rule's author what to fix.
	values := toStringMap(params)
	for _, name := range templateutil.ExtParamNames(template.BodyContent) {
		if strings.TrimSpace(values[name]) == "" {
			return uuid.Nil, fmt.Errorf("template parameter %q has no value", name)
		}
	}

	message, err := m.app.SendOutgoingMessage(ctx, OutgoingMessageRequest{
		Account:    account,
		Contact:    contact,
		Type:       models.MessageTypeTemplate,
		Template:   &template,
		BodyParams: values,
	}, automationSendOptions())
	if err != nil {
		return uuid.Nil, err
	}
	return message.ID, nil
}

// automationSendOptions marks the message as automation-sent.
//
// The sender type matters beyond bookkeeping: SLA timers and chatbot
// inactivity reminders only count agent replies, and an automated nudge is not
// somebody answering.
func automationSendOptions() MessageSendOptions {
	return MessageSendOptions{
		SenderType:         models.SenderAutomation,
		BroadcastWebSocket: true,
		DispatchWebhook:    true,
		TrackSLA:           false,
		// Synchronous, so the run log records whether it actually went.
		Async: false,
	}
}

// automationTarget resolves the contact and the account to send from.
func (a *App) automationTarget(orgID, contactID uuid.UUID) (*models.Contact, *models.WhatsAppAccount, error) {
	var contact models.Contact
	if err := a.DB.Where("id = ? AND organization_id = ?", contactID, orgID).
		First(&contact).Error; err != nil {
		return nil, nil, fmt.Errorf("contact not found")
	}

	// The account the contact already talks to, so a reply lands in the same
	// thread rather than arriving from an unfamiliar number.
	account, err := a.resolveWhatsAppAccount(orgID, contact.WhatsAppAccount)
	if err != nil {
		return nil, nil, err
	}
	return &contact, account, nil
}

func toStringMap(params map[string]any) map[string]string {
	out := make(map[string]string, len(params))
	for key, value := range params {
		out[key] = fmt.Sprint(value)
	}
	return out
}

// automationAssigner routes a conversation to a team through the transfer
// queue, so the team's assignment strategy and business hours apply.
type automationAssigner struct{ app *App }

func (t automationAssigner) AssignToTeam(_ context.Context, orgID, contactID, teamID uuid.UUID) (string, error) {
	contact, account, err := t.app.automationTarget(orgID, contactID)
	if err != nil {
		return "", err
	}

	var team models.Team
	if err := t.app.DB.Where("id = ? AND organization_id = ?", teamID, orgID).
		First(&team).Error; err != nil {
		return "", fmt.Errorf("that team no longer exists")
	}

	// The outcome is whatever actually happened — queued, assigned, suppressed
	// out of hours, or already being handled. Reporting "queued" regardless is
	// how a rule whose transfer was silently suppressed looks like it worked.
	outcome := t.app.createTransferToTeam(account, contact, teamID, "Automation",
		models.TransferSourceAutomation)
	if outcome == transfers.Failed {
		return outcome.String(), fmt.Errorf("the transfer could not be created")
	}
	return outcome.String(), nil
}

var _ crmactions.Messenger = automationMessenger{}
var _ crmactions.Assigner = automationAssigner{}
