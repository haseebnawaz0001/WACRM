package models_test

import (
	"testing"

	"github.com/shridarpatil/whatomate/internal/models"
	"github.com/stretchr/testify/assert"
)

// Anything unrecognised lands in "other", which is visible in the role editor
// and obviously wrong — which is the point. A permission that quietly vanished
// into a blank section would be one nobody could grant.
func TestPermissionGroupFor_EveryCatalogPermissionIsGrouped(t *testing.T) {
	ungrouped := map[string]bool{}
	for _, permission := range models.DefaultPermissions() {
		if models.PermissionGroupFor(permission.Resource) == models.PermissionGroupOther {
			ungrouped[permission.Resource] = true
		}
	}

	assert.Empty(t, ungrouped,
		"these resources are not in any permission group: %v", keysOf(ungrouped))
}

func TestPermissionGroupFor_BucketsByAreaOfTheProduct(t *testing.T) {
	cases := map[string]string{
		models.ResourceChat:        models.PermissionGroupInbox,
		models.ResourceDeals:       models.PermissionGroupCRM,
		models.ResourceAutomations: models.PermissionGroupCRM,
		models.ResourceCampaigns:   models.PermissionGroupMessaging,
		models.ResourceCallLogs:    models.PermissionGroupCalling,
		models.ResourceReports:     models.PermissionGroupInsights,
		models.ResourceRoles:       models.PermissionGroupAdmin,
	}

	for resource, want := range cases {
		assert.Equal(t, want, models.PermissionGroupFor(resource), resource)
	}
}

func TestPermissionGroupFor_UnknownResourcesFallBackToOther(t *testing.T) {
	assert.Equal(t, models.PermissionGroupOther, models.PermissionGroupFor("teleportation"))
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}
