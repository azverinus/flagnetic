package delivery_test

import (
	"maps"
	"os"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/workspace"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

// ids is the one identifier source of the package's tests, so no two of
// their entities share an identifier.
var ids = testkit.IDs(testkit.Clock())

// project stores a project with its default environments and returns it.
func project(t *testing.T, key string) workspace.Project {
	t.Helper()
	p, err := workspace.NewProject(id.New[workspace.Project](ids), key, key, "", testkit.Start)
	if err != nil {
		t.Fatal(err)
	}
	ws := pgworkspace.NewStore(testkit.Pool(t))
	if err = ws.CreateProject(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	for _, e := range workspace.DefaultsOf(p, envIDs{ids}).Environments {
		if err = ws.CreateEnvironment(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

type envIDs struct{ src *id.Source }

func (i envIDs) Environment() workspace.EnvironmentID { return id.New[workspace.Environment](i.src) }
func (i envIDs) UnitType() workspace.UnitTypeID       { return id.New[workspace.UnitType](i.src) }
func (i envIDs) Attribute() workspace.AttributeID     { return id.New[workspace.Attribute](i.src) }

func TestARulesetVersionStartsAtOneAndMovesOnAProjectAtATime(t *testing.T) {
	store := pgdelivery.NewStore(testkit.Pool(t))
	mobile := project(t, "mobile")
	web := project(t, "web")
	versions := func(p workspace.Project) map[string]int64 {
		v, err := store.RulesetVersions(t.Context(), p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if got, want := versions(mobile), map[string]int64{"development": 1, "staging": 1, "production": 1}; !maps.Equal(got, want) {
		t.Errorf("new = %v, want %v", got, want)
	}
	if err := store.BumpProject(t.Context(), mobile.ID); err != nil {
		t.Fatal(err)
	}
	if got, want := versions(mobile), map[string]int64{"development": 2, "staging": 2, "production": 2}; !maps.Equal(got, want) {
		t.Errorf("after bumping the project = %v, want %v", got, want)
	}
	if got, want := versions(web), map[string]int64{"development": 1, "staging": 1, "production": 1}; !maps.Equal(got, want) {
		t.Errorf("another project = %v, want %v untouched", got, want)
	}
}
