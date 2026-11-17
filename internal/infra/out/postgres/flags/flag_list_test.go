package flags

import (
	"testing"
	"time"

	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

func TestFlagsQueryRefusesWhatItHasNoSQLFor(t *testing.T) {
	env := workspace.EnvironmentID{}
	cases := map[string]appflags.FlagQuery{
		"unknown sort":  {Sort: "colour"},
		"unknown state": {Sort: appflags.SortByKey, In: &env, State: "sideways"},
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := flagsQuery(q); err == nil {
				t.Fatal("a query nothing can order or filter by was built")
			}
		})
	}
}

func TestFlagsQueryBuildsEverySort(t *testing.T) {
	cursor := appflags.FlagCursor{Key: "k", Name: "n", UpdatedAt: time.Unix(1, 0)}
	for _, sort := range []appflags.FlagSort{appflags.SortByKey, appflags.SortByName, appflags.SortByUpdated} {
		if _, _, err := flagsQuery(appflags.FlagQuery{Sort: sort, After: &cursor}); err != nil {
			t.Errorf("sort %q: %v", sort, err)
		}
	}
}
