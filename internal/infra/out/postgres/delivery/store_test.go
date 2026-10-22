package delivery_test

import (
	"maps"
	"os"
	"testing"
	"time"

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

// project stores a project with its default environments and returns it
// with them.
func project(t *testing.T, key string) (workspace.Project, []workspace.Environment) {
	t.Helper()
	p, err := workspace.NewProject(id.New[workspace.Project](ids), key, key, "", testkit.Start)
	if err != nil {
		t.Fatal(err)
	}
	ws := pgworkspace.NewStore(testkit.Pool(t))
	if err = ws.CreateProject(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	envs := workspace.DefaultsOf(p, envIDs{ids}).Environments
	for _, e := range envs {
		if err = ws.CreateEnvironment(t.Context(), e); err != nil {
			t.Fatal(err)
		}
	}
	return p, envs
}

type envIDs struct{ src *id.Source }

func (i envIDs) Environment() workspace.EnvironmentID { return id.New[workspace.Environment](i.src) }
func (i envIDs) UnitType() workspace.UnitTypeID       { return id.New[workspace.UnitType](i.src) }
func (i envIDs) Attribute() workspace.AttributeID     { return id.New[workspace.Attribute](i.src) }

func TestARulesetVersionStartsAtOneAndMovesOnAProjectOrAnEnvironmentAtATime(t *testing.T) {
	store := pgdelivery.NewStore(testkit.Pool(t))
	mobile, envs := project(t, "mobile")
	web, _ := project(t, "web")
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
	at := testkit.Start
	millis := at.UnixMilli()
	if err := store.BumpProject(t.Context(), mobile.ID, at); err != nil {
		t.Fatal(err)
	}
	if got, want := versions(mobile), map[string]int64{"development": millis, "staging": millis, "production": millis}; !maps.Equal(got, want) {
		t.Errorf("after bumping the project = %v, want the clock's milliseconds %v", got, want)
	}
	if got, want := versions(web), map[string]int64{"development": 1, "staging": 1, "production": 1}; !maps.Equal(got, want) {
		t.Errorf("another project = %v, want %v untouched", got, want)
	}
	var staging workspace.Environment
	for _, e := range envs {
		if e.Key == "staging" {
			staging = e
		}
	}
	if err := store.BumpEnvironment(t.Context(), staging.ID, at); err != nil {
		t.Fatal(err)
	}
	if got, want := versions(mobile), map[string]int64{"development": millis, "staging": millis + 1, "production": millis}; !maps.Equal(got, want) {
		t.Errorf("after bumping staging in the same millisecond = %v, want %v", got, want)
	}
	if err := store.BumpProject(t.Context(), mobile.ID, at.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, want := versions(mobile), map[string]int64{"development": millis + 1, "staging": millis + 2, "production": millis + 1}; !maps.Equal(got, want) {
		t.Errorf("after bumping the project with the clock set back = %v, want each one up %v", got, want)
	}
	later := at.Add(time.Second)
	if err := store.BumpEnvironment(t.Context(), staging.ID, later); err != nil {
		t.Fatal(err)
	}
	if got, want := versions(mobile)["staging"], later.UnixMilli(); got != want {
		t.Errorf("staging after the clock moved on = %d, want the clock's %d", got, want)
	}
}
