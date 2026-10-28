package governance_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/governance"
)

// auditActions returns every audit action the application records: the
// string literals of internal/app shaped as an action, entity.verb, other
// than permissions and event types, which are shaped alike.
func auditActions(t *testing.T) []string {
	t.Helper()
	shape := regexp.MustCompile(`^[a-z][a-z_]*\.[a-z][a-z_]*$`)
	found := map[string]bool{}
	err := filepath.WalkDir("../../app", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil && shape.MatchString(s) {
				if _, permission := access.PermissionNamed(s); !permission {
					found[s] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for a := range found {
		out = append(out, a)
	}
	slices.Sort(out)
	return out
}

// TestEveryAuditActionEitherCausesEventsOrIsListedAsCausingOnlyTheEntry
// keeps the catalogue complete as writes are added: a new action must be
// given its events or be declared entry-only.
func TestEveryAuditActionEitherCausesEventsOrIsListedAsCausingOnlyTheEntry(t *testing.T) {
	entryOnly := governance.EntryOnlyActions()
	catalogue := map[string]bool{}
	for _, e := range governance.EventCatalogue() {
		catalogue[e.Type] = true
	}
	// An entry deriving every kind of event, so that an action's events are
	// told by its name alone.
	before := configuration(false)
	after := configuration(true, map[string]any{"id": "r1"})
	after["kind"], after["apply_at"], after["reason"] = "srm", "2026-03-03T09:00:00Z", "why"

	for _, action := range auditActions(t) {
		events := governance.DeriveEvents(recorded(action, before, after))
		derives := len(events) > 1
		listed := slices.Contains(entryOnly, action)
		switch {
		case derives && listed:
			t.Errorf("%s causes events %v and is listed as causing only audit.entry", action, types(events))
		case !derives && !listed && catalogue[action]:
			// An event type itself, such as webhook.disabled: written by its
			// use case, not derived from an entry.
		case !derives && !listed:
			t.Errorf("%s causes only audit.entry: derive its events or list it in EntryOnlyActions", action)
		}
	}
	for _, action := range entryOnly {
		if !slices.Contains(auditActions(t), action) {
			t.Errorf("%s is listed as causing only audit.entry but no write records it", action)
		}
	}
}
