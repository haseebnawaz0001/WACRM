package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/models"
)

// CallContactMatcher answers the IVR's CRM condition node (plan 10, 4.4).
//
// It lives here rather than in internal/calling because compiling a contact
// filter needs the organization's custom fields, pipeline stages and saved
// segments, which that package cannot import without a cycle. The same shape as
// automation's Messenger and Assigner.
type CallContactMatcher struct{ app *App }

// CallMatcher builds the matcher the calling manager is given at startup.
func (a *App) CallMatcher() *CallContactMatcher { return &CallContactMatcher{app: a} }

// Matches reports whether one contact satisfies a filter.
//
// The question is asked mid-call, one contact at a time, so it compiles the
// filter and adds "AND contacts.id = ?" rather than reimplementing the
// operators — the IVR then agrees with segments and automation by construction.
func (m *CallContactMatcher) Matches(ctx context.Context, orgID, contactID uuid.UUID,
	raw map[string]any) (bool, error) {

	if len(raw) == 0 || contactID == uuid.Nil {
		return false, nil
	}

	encoded, err := json.Marshal(raw)
	if err != nil {
		return false, fmt.Errorf("ivr condition: %w", err)
	}
	var filter contactquery.Filter
	if err := json.Unmarshal(encoded, &filter); err != nil {
		return false, fmt.Errorf("ivr condition: %w", err)
	}
	if filter.IsEmpty() {
		return false, nil
	}

	registry, err := m.app.contactRegistry(orgID)
	if err != nil {
		return false, err
	}

	viewer := contactquery.Viewer{
		OrgID: orgID,
		// An IVR flow is not a person: it sees whatever its condition names,
		// or a call would route differently depending on who saved the flow.
		CanSeeAllContacts: true,
		Location:          m.app.OrgLocation(orgID),
	}

	query, err := contactquery.Apply(
		m.app.DB.WithContext(ctx).Model(&models.Contact{}), registry, viewer, filter)
	if err != nil {
		return false, err
	}

	var count int64
	if err := query.Where("contacts.id = ?", contactID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}
