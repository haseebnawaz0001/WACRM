package handlers

import (
	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/models"
)

// Exports for the external handlers_test package.
//
// The tests live in handlers_test so they exercise the package the way its
// callers do. That leaves unexported helpers unreachable, and the ones worth
// testing directly are re-exported here rather than made public on the App —
// a method the whole product can call is a different decision from one a test
// can reach.

// ResolveParamsForContactsForTest exposes campaign parameter resolution.
func (a *App) ResolveParamsForContactsForTest(orgID uuid.UUID, contacts []models.Contact, mappings map[string]ParamMapping) (map[uuid.UUID]map[string]string, error) {
	return a.resolveParamsForContacts(orgID, contacts, mappings)
}
