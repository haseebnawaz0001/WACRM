package main

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

var validMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true,
	"HEAD": true, "OPTIONS": true,
}

func TestRoutePermissions_EntriesAreWellFormed(t *testing.T) {
	for route, access := range routePermissions {
		method, path, found := strings.Cut(route, " ")
		if !found {
			t.Errorf("route key %q is not %q", route, "METHOD /path")
			continue
		}
		if !validMethods[method] {
			t.Errorf("route %q has unknown method %q", route, method)
		}
		if !strings.HasPrefix(path, "/api") {
			t.Errorf("route %q is not an /api route; the table only covers the API", route)
		}
		if access.kind == accessPermission && (access.resource == "" || access.action == "") {
			t.Errorf("route %q requires a permission but names no resource/action", route)
		}
		if access.kind != accessPermission && (access.resource != "" || access.action != "") {
			t.Errorf("route %q is marked public/self/scoped but also names a permission", route)
		}
	}
}

// TestRoutePermissions_UnauthenticatedRoutesAreTheExpectedSet pins the set of
// endpoints reachable with no credentials at all.
//
// This is the list an attacker starts from, so it should never grow by
// accident. Adding a route here is a deliberate edit to this test, reviewed on
// its own, rather than a one-word change in a table of three hundred lines.
func TestRoutePermissions_UnauthenticatedRoutesAreTheExpectedSet(t *testing.T) {
	want := []string{
		"GET /api/auth/sso/providers",
		"GET /api/auth/sso/{provider}/callback",
		"GET /api/auth/sso/{provider}/init",
		"GET /api/custom-actions/redirect/{token}",
		"GET /api/embedded-signup/config",
		"GET /api/webhook",
		"POST /api/auth/login",
		"POST /api/auth/logout",
		"POST /api/auth/refresh",
		"POST /api/auth/register",
		"POST /api/webhook",
	}
	sort.Strings(want)

	got, _, _ := unprotectedRoutes()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("public routes changed.\n got: %v\nwant: %v", got, want)
	}
}

// TestRoutePermissions_SelfAndScopedRoutesStayNarrow does the same for the
// routes any authenticated user may call. These are not public, but they skip
// the permission system, so the set is worth pinning too.
func TestRoutePermissions_SelfAndScopedRoutesStayNarrow(t *testing.T) {
	_, selfRoutes, scopedRoutes := unprotectedRoutes()

	wantSelf := []string{
		"GET /api/auth/ws-token",
		"GET /api/me",
		"GET /api/me/organizations",
		"GET /api/notifications",
		"GET /api/notifications/unread-count",
		"GET /api/org/settings",
		"GET /api/organizations/current",
		"POST /api/auth/switch-org",
		"POST /api/notifications/read-all",
		"POST /api/notifications/{id}/read",
		"PUT /api/me/availability",
		"PUT /api/me/password",
		"PUT /api/me/settings",
	}
	sort.Strings(wantSelf)
	if !reflect.DeepEqual(selfRoutes, wantSelf) {
		t.Errorf("self routes changed.\n got: %v\nwant: %v", selfRoutes, wantSelf)
	}

	// Scoped routes decide visibility per row (S9). Listing contacts is the
	// only one that has no flat permission at all: an agent with no
	// contacts:read still sees the contacts assigned to them.
	wantScoped := []string{"GET /api/contacts"}
	if !reflect.DeepEqual(scopedRoutes, wantScoped) {
		t.Errorf("scoped routes changed.\n got: %v\nwant: %v", scopedRoutes, wantScoped)
	}
}

func TestPermissionForRoute(t *testing.T) {
	resource, action, ok := permissionForRoute("GET", "/api/campaigns")
	if !ok {
		t.Fatal("GET /api/campaigns should require a permission")
	}
	if resource != "campaigns" || action != "read" {
		t.Errorf("got %s:%s, want campaigns:read", resource, action)
	}

	if _, _, ok := permissionForRoute("POST", "/api/auth/login"); ok {
		t.Error("login is public; it should report no permission")
	}
	if _, _, ok := permissionForRoute("GET", "/api/nope"); ok {
		t.Error("an unknown route should report no permission")
	}
}
