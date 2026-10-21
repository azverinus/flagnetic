package workspace_test

import (
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var now = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func newProjectID() workspace.ProjectID {
	return id.New[workspace.Project](id.NewDeterministicSource(fixedClock{}))
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return now }

// violation returns the code of an invalid error and of its first
// violation, with its pointer.
func violation(err error) (code, pointer, violationCode string) {
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid {
		return "", "", ""
	}
	if len(e.Violations) == 0 {
		return e.Code, "", ""
	}
	return e.Code, e.Violations[0].Pointer, e.Violations[0].Code
}

func TestAKeyMatchesThePattern(t *testing.T) {
	for _, key := range []string{"m", "mobile", "mobile-app", "mobile_app2", "m" + strings.Repeat("x", 63)} {
		if _, err := workspace.NewProject(newProjectID(), key, "Mobile", "", now); err != nil {
			t.Errorf("key %q refused: %v", key, err)
		}
	}
	for _, key := range []string{"", "Mobile", "1mobile", "-mobile", "_mobile", "mobile app", "mobile.app", "m" + strings.Repeat("x", 64), "mobilé"} {
		_, err := workspace.NewProject(newProjectID(), key, "Mobile", "", now)
		if code, ptr, vc := violation(err); code != "invalid_project" || ptr != "/key" || vc != "invalid_key" {
			t.Errorf("key %q: refusal = %s %s %s, want invalid_project /key invalid_key", key, code, ptr, vc)
		}
	}
}

func TestANameHasOneTo200CharactersAndIsNotBlank(t *testing.T) {
	p, err := workspace.NewProject(newProjectID(), "mobile", "  Mobile apps ", "", now)
	if err != nil || p.Name != "Mobile apps" {
		t.Fatalf("project = %+v, %v; want the name trimmed", p, err)
	}
	if _, err := workspace.NewProject(newProjectID(), "mobile", strings.Repeat("ж", 200), "", now); err != nil {
		t.Errorf("a name of 200 characters refused: %v", err)
	}
	for _, name := range []string{"", "   ", strings.Repeat("ж", 201)} {
		_, err := workspace.NewProject(newProjectID(), "mobile", name, "", now)
		if code, ptr, vc := violation(err); code != "invalid_project" || ptr != "/name" || vc != "invalid_name" {
			t.Errorf("name of %d characters: refusal = %s %s %s, want invalid_project /name invalid_name", len([]rune(name)), code, ptr, vc)
		}
	}
}

func TestADescriptionHasAtMost2000Characters(t *testing.T) {
	if _, err := workspace.NewProject(newProjectID(), "mobile", "Mobile", strings.Repeat("ж", 2000), now); err != nil {
		t.Errorf("a description of 2,000 characters refused: %v", err)
	}
	_, err := workspace.NewProject(newProjectID(), "mobile", "Mobile", strings.Repeat("ж", 2001), now)
	if code, ptr, vc := violation(err); code != "invalid_project" || ptr != "/description" || vc != "too_long" {
		t.Errorf("refusal = %s %s %s, want invalid_project /description too_long", code, ptr, vc)
	}
}

func TestAProjectKeyIsImmutableAndItsNameAndDescriptionChange(t *testing.T) {
	p, err := workspace.NewProject(newProjectID(), "mobile", "Mobile", "", now)
	if err != nil {
		t.Fatal(err)
	}
	key := "renamed"
	if _, _, refused := p.Change(workspace.ProjectChange{Key: &key}, now); violationCode(refused) != "key_immutable" {
		t.Errorf("changing the key: refusal = %v, want key_immutable", refused)
	}
	same := "mobile"
	if _, _, refused := p.Change(workspace.ProjectChange{Key: &same}, now); violationCode(refused) != "key_immutable" {
		t.Errorf("sending the key: refusal = %v, want key_immutable", refused)
	}

	name, description := "Mobile apps", "iOS and Android"
	later := now.Add(time.Hour)
	changed, ok, err := p.Change(workspace.ProjectChange{Name: &name, Description: &description}, later)
	if err != nil || !ok || changed.Name != name || changed.Description != description || changed.Key != "mobile" ||
		!changed.UpdatedAt.Equal(later) || !changed.CreatedAt.Equal(now) {
		t.Errorf("changed = %+v, %v", changed, err)
	}
	if _, _, refused := p.Change(workspace.ProjectChange{Name: new(string)}, later); violationCode(refused) != "invalid_project" {
		t.Errorf("a blank name: refusal = %v, want invalid_project", refused)
	}
	unchanged, ok, err := changed.Change(workspace.ProjectChange{Name: &name}, later.Add(time.Hour))
	if err != nil || ok || unchanged != changed {
		t.Errorf("the same name again: %+v changed %v, %v; want no change", unchanged, ok, err)
	}
}

func violationCode(err error) string {
	code, _, _ := violation(err)
	return code
}
