// Package orgseed gives a new organization everything it needs to be usable
// (plan 10, S8).
//
// Seeding used to be spread across the organization handler, the first-run
// admin bootstrap and a handful of migrations, each aware of a different
// subset. The result was that an organization created through the UI had roles
// but no contact fields, no task types and no pipeline, and only became whole
// the next time a migration happened to run. Everything a new organization
// needs is listed here, once, and every path that creates one calls it.
package orgseed

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/customfields"
	"github.com/shridarpatil/whatomate/internal/deals"
	"github.com/shridarpatil/whatomate/internal/tasks"
	"gorm.io/gorm"
)

// step is one thing a new organization needs.
type step struct {
	name string
	run  func(tx *gorm.DB, orgID uuid.UUID) error
}

// steps are the CRM defaults. Roles and permissions are seeded separately,
// before this runs, because they are what everything else is checked against.
//
// Every step is written to be safe to re-run, so this is also what brings an
// organization created before a feature shipped up to date.
var steps = []step{
	{"contact fields", customfields.SeedOrganization},
	{"task types", tasks.SeedOrganization},
	{"default pipeline", deals.SeedOrganization},
}

// Seed fills in an organization's CRM defaults.
//
// Pass the transaction that created the organization, so a half-seeded
// organization can never be committed.
func Seed(tx *gorm.DB, orgID uuid.UUID) error {
	for _, s := range steps {
		if err := s.run(tx, orgID); err != nil {
			return fmt.Errorf("orgseed: %s: %w", s.name, err)
		}
	}
	return nil
}

// Steps names what Seed does, for tests and for anything that wants to report
// what a new organization gets.
func Steps() []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		out = append(out, s.name)
	}
	return out
}
