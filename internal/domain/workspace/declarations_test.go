package workspace_test

import (
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func ids() *id.Source { return id.NewDeterministicSource(fixedClock{}) }

func conflictCode(err error) string {
	if e, ok := apperrors.From(err); ok && e.Kind == apperrors.KindConflict {
		return e.Code
	}
	return ""
}

func TestAnEnvironmentHasAKeyAndAName(t *testing.T) {
	project := newProjectID()
	e, err := workspace.NewEnvironment(id.New[workspace.Environment](ids()), project, "qa", " QA ", 4, now)
	if err != nil || e.Key != "qa" || e.Name != "QA" || e.SortOrder != 4 || e.Project != project {
		t.Fatalf("environment = %+v, %v", e, err)
	}
	for _, key := range []string{"", "QA", "9qa", "q a"} {
		_, refused := workspace.NewEnvironment(id.New[workspace.Environment](ids()), project, key, "QA", 1, now)
		if code, ptr, vc := violation(refused); code != "invalid_environment" || ptr != "/key" || vc != "invalid_key" {
			t.Errorf("key %q: refusal = %s %s %s", key, code, ptr, vc)
		}
	}
	_, err = workspace.NewEnvironment(id.New[workspace.Environment](ids()), project, "qa", "", 1, now)
	if code, ptr, _ := violation(err); code != "invalid_environment" || ptr != "/name" {
		t.Errorf("blank name: refusal = %s %s", code, ptr)
	}
}

func TestAnEnvironmentIsRenamedButKeepsItsKey(t *testing.T) {
	e, _ := workspace.NewEnvironment(id.New[workspace.Environment](ids()), newProjectID(), "qa", "QA", 4, now)
	key, name, later := "test", "Quality", now.Add(time.Hour)
	if _, _, err := e.Change(workspace.EnvironmentChange{Key: &key}, later); violationCode(err) != "key_immutable" {
		t.Errorf("a new key: refusal = %v", err)
	}
	changed, ok, err := e.Change(workspace.EnvironmentChange{Name: &name}, later)
	if err != nil || !ok || changed.Name != name || changed.Key != "qa" || !changed.UpdatedAt.Equal(later) {
		t.Errorf("renamed = %+v %v %v", changed, ok, err)
	}
	if _, ok, err := changed.Change(workspace.EnvironmentChange{Name: &name}, later); err != nil || ok {
		t.Errorf("the same name again changed %v, %v", ok, err)
	}
	blank := " "
	if _, _, err := e.Change(workspace.EnvironmentChange{Name: &blank}, later); violationCode(err) != "invalid_environment" {
		t.Errorf("a blank name: refusal = %v", err)
	}
}

func TestAnEnvironmentIsProtectedAndUnprotected(t *testing.T) {
	e, _ := workspace.NewEnvironment(id.New[workspace.Environment](ids()), newProjectID(), "qa", "QA", 4, now)
	if e.Protected {
		t.Fatal("a new environment is protected")
	}
	later := now.Add(time.Hour)
	protected, changed := e.Protect(true, later)
	if !changed || !protected.Protected || protected.Version != 2 || !protected.UpdatedAt.Equal(later) || protected.Name != "QA" {
		t.Errorf("protected = %+v, %v", protected, changed)
	}
	if same, again := protected.Protect(true, later.Add(time.Hour)); again || same != protected {
		t.Errorf("protecting again = %+v, %v, want no change", same, again)
	}
	unprotected, changed := protected.Protect(false, later)
	if !changed || unprotected.Protected || unprotected.Version != 3 {
		t.Errorf("unprotected = %+v, %v", unprotected, changed)
	}
}

func TestTheLastEnvironmentCannotBeDeleted(t *testing.T) {
	e, _ := workspace.NewEnvironment(id.New[workspace.Environment](ids()), newProjectID(), "qa", "QA", 4, now)
	if err := workspace.CanDeleteEnvironment(e, 2); err != nil {
		t.Errorf("one of two: %v", err)
	}
	if got := conflictCode(workspace.CanDeleteEnvironment(e, 1)); got != "last_environment" {
		t.Errorf("the last one: refusal = %q, want last_environment", got)
	}
}

func TestAProtectedEnvironmentCannotBeDeleted(t *testing.T) {
	e, _ := workspace.NewEnvironment(id.New[workspace.Environment](ids()), newProjectID(), "qa", "QA", 4, now)
	protected, _ := e.Protect(true, now)
	if got := conflictCode(workspace.CanDeleteEnvironment(protected, 2)); got != "environment_protected" {
		t.Errorf("one of two, protected: refusal = %q, want environment_protected", got)
	}
	if got := conflictCode(workspace.CanDeleteEnvironment(protected, 1)); got != "last_environment" {
		t.Errorf("the last one, protected: refusal = %q, want last_environment, which unprotecting would not lift", got)
	}
	unprotected, _ := protected.Protect(false, now)
	if err := workspace.CanDeleteEnvironment(unprotected, 2); err != nil {
		t.Errorf("one of two, unprotected again: %v", err)
	}
}

func TestAUnitTypeHasAKeyANameAndADescription(t *testing.T) {
	u, err := workspace.NewUnitType(id.New[workspace.UnitType](ids()), newProjectID(), "organization", "Organization", "A customer account.", now)
	if err != nil || u.Key != "organization" || u.Description != "A customer account." {
		t.Fatalf("unit type = %+v, %v", u, err)
	}
	_, err = workspace.NewUnitType(id.New[workspace.UnitType](ids()), newProjectID(), "a b", "", strings.Repeat("x", 2001), now)
	e, _ := apperrors.From(err)
	if e == nil || e.Code != "invalid_unit_type" || len(e.Violations) != 3 {
		t.Errorf("refusal = %+v, want invalid_unit_type naming the key, name and description", e)
	}
	key, empty := "other", ""
	if _, _, refused := u.Change(workspace.UnitTypeChange{Key: &key}, now); violationCode(refused) != "key_immutable" {
		t.Errorf("a new key: refusal = %v", refused)
	}
	changed, ok, err := u.Change(workspace.UnitTypeChange{Description: &empty}, now.Add(time.Hour))
	if err != nil || !ok || changed.Description != "" || changed.Name != "Organization" {
		t.Errorf("changed = %+v %v %v", changed, ok, err)
	}
}

func TestAnAttributeIsTypedAndItsTypeNeverChanges(t *testing.T) {
	project := newProjectID()
	for _, typ := range workspace.AttributeTypes() {
		if _, err := workspace.NewAttribute(id.New[workspace.Attribute](ids()), project, "tier", "Tier", "", typ, now); err != nil {
			t.Errorf("type %s refused: %v", typ, err)
		}
	}
	_, err := workspace.NewAttribute(id.New[workspace.Attribute](ids()), project, "tier", "Tier", "", "json", now)
	if code, ptr, vc := violation(err); code != "invalid_attribute" || ptr != "/type" || vc != "invalid_type" {
		t.Errorf("type json: refusal = %s %s %s", code, ptr, vc)
	}
	_, err = workspace.NewAttribute(id.New[workspace.Attribute](ids()), project, "discovered", "Discovered", "", workspace.TypeString, now)
	if code, ptr, vc := violation(err); code != "invalid_attribute" || ptr != "/key" || vc != "reserved_key" {
		t.Errorf("key discovered: refusal = %s %s %s", code, ptr, vc)
	}

	a, _ := workspace.NewAttribute(id.New[workspace.Attribute](ids()), project, "tier", "Tier", "", workspace.TypeString, now)
	same := workspace.TypeString
	if _, _, refused := a.Change(workspace.AttributeChange{Type: &same}, now); violationCode(refused) != "type_immutable" {
		t.Errorf("sending the type: refusal = %v, want type_immutable", refused)
	}
	key := "plan"
	if _, _, refused := a.Change(workspace.AttributeChange{Key: &key}, now); violationCode(refused) != "key_immutable" {
		t.Errorf("a new key: refusal = %v", refused)
	}
	name := "Plan tier"
	changed, ok, err := a.Change(workspace.AttributeChange{Name: &name}, now)
	if err != nil || !ok || changed.Name != name || changed.Type != workspace.TypeString {
		t.Errorf("renamed = %+v %v %v", changed, ok, err)
	}
	if err := a.CanDelete(); err != nil {
		t.Errorf("a declared attribute cannot be deleted: %v", err)
	}
}

func TestStandardAttributesAreRenamedButNeverDeleted(t *testing.T) {
	p, _ := workspace.NewProject(newProjectID(), "mobile", "Mobile", "", now)
	d := workspace.DefaultsOf(p, defaultIDs{ids()})
	var appVersion workspace.Attribute
	for _, a := range d.Attributes {
		if a.Key == "app_version" {
			appVersion = a
		}
	}
	if !appVersion.Standard || appVersion.Type != workspace.TypeSemver {
		t.Fatalf("app_version = %+v", appVersion)
	}
	if got := conflictCode(appVersion.CanDelete()); got != "standard_attribute" {
		t.Errorf("deleting: refusal = %q, want standard_attribute", got)
	}
	name := "Mobile app version"
	changed, ok, err := appVersion.Change(workspace.AttributeChange{Name: &name}, now)
	if err != nil || !ok || !changed.Standard {
		t.Errorf("renamed = %+v %v %v", changed, ok, err)
	}
}

func TestANewProjectHasItsDefaults(t *testing.T) {
	p, _ := workspace.NewProject(newProjectID(), "mobile", "Mobile", "", now)
	d := workspace.DefaultsOf(p, defaultIDs{ids()})
	var envs, units []string
	for _, e := range d.Environments {
		envs = append(envs, e.Key)
	}
	for _, u := range d.UnitTypes {
		units = append(units, u.Key)
	}
	if strings.Join(envs, ",") != "development,staging,production" || strings.Join(units, ",") != "user,anonymous" ||
		len(d.Attributes) != 10 {
		t.Errorf("defaults = %v %v %d attributes", envs, units, len(d.Attributes))
	}
	for _, e := range d.Environments {
		if e.Protected != (e.Key == "production") {
			t.Errorf("%s protected = %v, want production alone protected", e.Key, e.Protected)
		}
	}
	seen := map[string]bool{}
	for _, a := range d.Attributes {
		seen[a.ID.String()] = true
	}
	if len(seen) != 10 {
		t.Error("attributes share identifiers")
	}
}

type defaultIDs struct{ src *id.Source }

func (i defaultIDs) Environment() workspace.EnvironmentID {
	return id.New[workspace.Environment](i.src)
}
func (i defaultIDs) UnitType() workspace.UnitTypeID   { return id.New[workspace.UnitType](i.src) }
func (i defaultIDs) Attribute() workspace.AttributeID { return id.New[workspace.Attribute](i.src) }
