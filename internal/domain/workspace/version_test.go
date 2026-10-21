package workspace_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

func TestEveryWorkspaceEntityStartsAtVersionOneAndCountsItsChanges(t *testing.T) {
	later := now.Add(time.Hour)
	name, description := "Renamed", "Described"

	p, _ := workspace.NewProject(newProjectID(), "mobile", "Mobile", "", now)
	e, _ := workspace.NewEnvironment(id.New[workspace.Environment](ids()), p.ID, "qa", "QA", 4, now)
	u, _ := workspace.NewUnitType(id.New[workspace.UnitType](ids()), p.ID, "organization", "Organization", "", now)
	a, _ := workspace.NewAttribute(id.New[workspace.Attribute](ids()), p.ID, "plan", "Plan", "", workspace.TypeString, now)
	if p.Version != 1 || e.Version != 1 || u.Version != 1 || a.Version != 1 {
		t.Fatalf("created at versions %d %d %d %d, want 1", p.Version, e.Version, u.Version, a.Version)
	}
	d := workspace.DefaultsOf(p, defaultIDs{ids()})
	for _, x := range d.Environments {
		if x.Version != 1 {
			t.Errorf("default environment %s at version %d, want 1", x.Key, x.Version)
		}
	}
	for _, x := range d.UnitTypes {
		if x.Version != 1 {
			t.Errorf("default unit type %s at version %d, want 1", x.Key, x.Version)
		}
	}
	for _, x := range d.Attributes {
		if x.Version != 1 {
			t.Errorf("standard attribute %s at version %d, want 1", x.Key, x.Version)
		}
	}

	p2, _, _ := p.Change(workspace.ProjectChange{Name: &name}, later)
	e2, _, _ := e.Change(workspace.EnvironmentChange{Name: &name}, later)
	u2, _, _ := u.Change(workspace.UnitTypeChange{Description: &description}, later)
	a2, _, _ := a.Change(workspace.AttributeChange{Name: &name}, later)
	if p2.Version != 2 || e2.Version != 2 || u2.Version != 2 || a2.Version != 2 {
		t.Errorf("changed at versions %d %d %d %d, want 2", p2.Version, e2.Version, u2.Version, a2.Version)
	}

	p3, _, _ := p2.Change(workspace.ProjectChange{Name: &name}, later)
	e3, _, _ := e2.Change(workspace.EnvironmentChange{Name: &name}, later)
	u3, _, _ := u2.Change(workspace.UnitTypeChange{Description: &description}, later)
	a3, _, _ := a2.Change(workspace.AttributeChange{Name: &name}, later)
	if p3.Version != 2 || e3.Version != 2 || u3.Version != 2 || a3.Version != 2 {
		t.Errorf("a change to the same values moved versions to %d %d %d %d, want 2", p3.Version, e3.Version, u3.Version, a3.Version)
	}
}
