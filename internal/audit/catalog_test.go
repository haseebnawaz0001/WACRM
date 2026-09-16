package audit_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shridarpatil/whatomate/internal/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoRoot walks up from this package to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find the module root")
	return ""
}

// modelsResourceConstants reads models.Resource* so a call site written as
// models.ResourceSegments resolves to the string it actually stores.
func modelsResourceConstants(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}

	modelsDir := filepath.Join(root, "internal", "models")
	entries, err := os.ReadDir(modelsDir)
	require.NoError(t, err)

	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(modelsDir, entry.Name()), nil, 0)
		require.NoError(t, err)

		ast.Inspect(file, func(n ast.Node) bool {
			spec, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range spec.Names {
				if !strings.HasPrefix(name.Name, "Resource") || i >= len(spec.Values) {
					continue
				}
				if lit, ok := spec.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if value, err := strconv.Unquote(lit.Value); err == nil {
						out[name.Name] = value
					}
				}
			}
			return true
		})
	}
	return out
}

// auditedResourceTypes reads every logAudit / LogAudit call site and returns the
// resource type each one writes.
func auditedResourceTypes(t *testing.T, root string, consts map[string]string) map[string]string {
	t.Helper()
	out := map[string]string{}

	fset := token.NewFileSet()
	err := filepath.Walk(filepath.Join(root, "internal"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			// a.logAudit(orgID, userID, resourceType, ...) and
			// audit.LogAudit(db, orgID, userID, userName, resourceType, ...)
			var argIndex int
			switch sel.Sel.Name {
			case "logAudit":
				argIndex = 2
			case "LogAudit":
				argIndex = 4
			default:
				return true
			}
			if len(call.Args) <= argIndex {
				return true
			}

			where := fset.Position(call.Pos()).String()
			switch arg := call.Args[argIndex].(type) {
			case *ast.BasicLit:
				if arg.Kind == token.STRING {
					if value, err := strconv.Unquote(arg.Value); err == nil {
						out[value] = where
					}
				}
			case *ast.SelectorExpr:
				if value, known := consts[arg.Sel.Name]; known {
					out[value] = where
				}
			}
			return true
		})
		return nil
	})
	require.NoError(t, err)
	return out
}

// Plan 10, S9: the audit log's filters are a projection of what the server
// writes, not a list somebody keeps by hand.
//
// The Vue component's hardcoded picker offered ten resources against fifteen
// the server actually wrote, so five kinds of change could be recorded but
// never found — and one of the ten was never written at all, so choosing it
// always returned nothing. Reading the call sites is the only check that stays
// true as handlers are added.
func TestResourceCatalogCoversEveryAuditedResource(t *testing.T) {
	root := repoRoot(t)
	consts := modelsResourceConstants(t, root)
	audited := auditedResourceTypes(t, root, consts)

	require.NotEmpty(t, audited, "the scan must find the audit call sites at all")

	for resource, where := range audited {
		assert.True(t, audit.KnownResourceType(resource),
			"%s is written to the audit log at %s but is not in the catalog, so it cannot be filtered for",
			resource, where)
	}
}

// The reverse: a catalog entry nothing writes is a filter that always returns
// nothing, which reads as "there is no history for this" rather than "this is
// not audited".
func TestResourceCatalogHasNoEntriesNobodyWrites(t *testing.T) {
	root := repoRoot(t)
	consts := modelsResourceConstants(t, root)
	audited := auditedResourceTypes(t, root, consts)

	for _, rt := range audit.ResourceTypes() {
		assert.Contains(t, audited, rt.Value,
			"%q is offered as a filter but nothing writes it, so the filter is always empty", rt.Value)
	}
}

func TestResourceTypesAreSortedByLabel(t *testing.T) {
	types := audit.ResourceTypes()
	require.NotEmpty(t, types)
	for i := 1; i < len(types); i++ {
		assert.LessOrEqual(t, types[i-1].Label, types[i].Label)
	}
}
