package management_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

// A draft or running experiment still assigns units of its unit type once
// started, so deleting that unit type is refused naming it; a stopped or an
// archived one assigns nobody and does not hold the unit type.
func TestDeletingAUnitTypeAnActiveExperimentAssignsIsRefused(t *testing.T) {
	f := startExperiments(t)
	declareUnitType(t, f.admin, f.project.Key, "device")
	for _, key := range []string{"drafted", "running", "stopped", "archived"} {
		body := validExperiment(key)
		body.UnitType = "device"
		createExperiment(t, f.admin, f.project.Key, body)
	}
	for _, status := range []experiments.Status{experiments.StatusRunning, experiments.StatusStopped, experiments.StatusArchived} {
		putInStatus(t, f, string(status), status)
	}

	res := do(f.admin.DeleteUnitTypeWithResponse(t.Context(), f.project.Key, "device", &management.DeleteUnitTypeParams{IfMatch: ifMatch(1)}))
	got := kindedReferencesOf(t, "a unit type a draft and a running experiment assign", res.StatusCode(), res.Body)
	if want := []string{"experiment drafted ", "experiment running "}; !slices.Equal(got, want) {
		t.Errorf("references = %q, want %q", got, want)
	}

	for _, key := range []string{"drafted", "running"} {
		putInStatus(t, f, key, experiments.StatusStopped)
	}
	res = do(f.admin.DeleteUnitTypeWithResponse(t.Context(), f.project.Key, "device", &management.DeleteUnitTypeParams{IfMatch: ifMatch(1)}))
	if res.StatusCode() != http.StatusNoContent {
		t.Errorf("deleting a unit type only stopped and archived experiments name = %d %s", res.StatusCode(), res.Body)
	}
}
