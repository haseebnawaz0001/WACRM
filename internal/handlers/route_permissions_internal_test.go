package handlers

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// routesWithoutPermissionCheck lists handlers that are intentionally reachable
// without a resource permission check, with the reason. Every other handler
// registered in cmd/wacrm/main.go must call requireAuth, requireAnyPermission,
// requirePermission or HasPermission.
var routesWithoutPermissionCheck = map[string]string{
	// Public or pre-authentication endpoints (whitelisted in the auth middleware).
	"HealthCheck":           "public health probe",
	"ReadyCheck":            "public readiness probe",
	"Login":                 "public: authentication",
	"Register":              "public: authentication",
	"Logout":                "clears the caller's own session",
	"RefreshToken":          "public: token refresh via cookie",
	"GetWSToken":            "issues a websocket token for the caller's own session",
	"GetPublicSSOProviders": "public: login page provider list",
	"InitSSO":               "public: SSO login flow",
	"CallbackSSO":           "public: SSO login flow",
	"WebhookVerify":         "public: Meta webhook verification (verify token)",
	"WebhookHandler":        "public: Meta webhook delivery (signature verified)",
	"WebSocketHandler":      "authenticates with a websocket token frame",

	// Self-service endpoints that only touch the caller's own data.
	"GetCurrentUser":            "self",
	"UpdateCurrentUserSettings": "self",
	"ChangePassword":            "self",
	"UpdateAvailability":        "self",
	"ListMyOrganizations":       "self: organizations the caller belongs to",
	"SwitchOrg":                 "self: validates membership of the target org",

	// Organization-wide, non-sensitive data every member needs to use the app.
	"GetOrganizationSettings": "timezone, date format, masking and calling flags; no secrets",
	"GetCurrentOrganization":  "id, name and slug of the caller's organization",
	"CustomActionRedirect":    "short-lived random token issued by ExecuteCustomAction",
}

var (
	// Matches both g.GET("/x", app.H) and wrapped forms like g.POST("/x", withRateLimit(app.H, ...)).
	routeRegexp       = regexp.MustCompile(`g\.(GET|POST|PUT|PATCH|DELETE)\("([^"]+)",\s*(?:\w+\()?app\.(\w+)`)
	handlerFuncRegexp = regexp.MustCompile(`(?m)^func \(a \*App\) (\w+)\(r \*fastglue\.Request\) error \{`)
	permissionCall    = regexp.MustCompile(`requireAuth\(|requireAnyPermission\(|requirePermission\(|HasPermission\(`)
)

// TestAllRoutesCheckPermissions guards against new API routes that forget the
// server-side permission check (the frontend hiding a page is not access
// control). It parses route registrations and handler bodies from source.
func TestAllRoutesCheckPermissions(t *testing.T) {
	mainSrc, err := os.ReadFile(filepath.Join("..", "..", "cmd", "wacrm", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	bodies := handlerBodies(t)

	var missing []string
	registered := map[string]bool{}
	for _, m := range routeRegexp.FindAllStringSubmatch(string(mainSrc), -1) {
		method, path, handler := m[1], m[2], m[3]
		registered[handler] = true
		if _, exempt := routesWithoutPermissionCheck[handler]; exempt {
			continue
		}
		body, ok := bodies[handler]
		if !ok {
			t.Errorf("route %s %s uses app.%s, but no handler with that name was found", method, path, handler)
			continue
		}
		if !permissionCall.MatchString(body) {
			missing = append(missing, method+" "+path+" -> "+handler)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("routes without a permission check (add requireAuth/requireAnyPermission, or document an exemption in routesWithoutPermissionCheck):\n  %s",
			strings.Join(missing, "\n  "))
	}

	// Keep the exemption list honest: entries must refer to registered routes.
	for handler := range routesWithoutPermissionCheck {
		if !registered[handler] {
			t.Errorf("routesWithoutPermissionCheck lists %s, which is not registered in main.go", handler)
		}
	}
}

// handlerBodies maps each App handler name to its source text.
func handlerBodies(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob handlers: %v", err)
	}
	bodies := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		text := string(src)
		locs := handlerFuncRegexp.FindAllStringSubmatchIndex(text, -1)
		for i, loc := range locs {
			end := len(text)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			bodies[text[loc[2]:loc[3]]] = text[loc[0]:end]
		}
	}
	return bodies
}
