package management_test

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func assignment(t *testing.T, c *management.ClientWithResponses, project, key, unitID string) *management.GetExperimentAssignmentResponse {
	t.Helper()
	return do(c.GetExperimentAssignmentWithResponse(t.Context(), project, key, &management.GetExperimentAssignmentParams{UnitId: unitID}))
}

func TestAnAssignmentIsComputedFromTheExperimentsOwnFields(t *testing.T) {
	f := startExperiments(t)
	body := validExperiment("split")
	body.Variations, body.TrafficAllocation = weights("a", 3000, "b", 7000), ptr(6000)
	e := createExperiment(t, f.admin, f.project.Key, body)
	viewer := testkit.Client(t, f.srv, testkit.GrantedToken(t, testkit.Pool(t), projectIDOf(f.project), access.RoleViewer))
	audit := auditActions(t, f.admin, f.project.Key)

	seen := map[string]bool{}
	for i := range 200 {
		unit := fmt.Sprintf("user-%d", i)
		res := assignment(t, viewer, f.project.Key, "split", unit)
		if res.JSON200 == nil {
			t.Fatalf("GET assignment %s = %d %s", unit, res.StatusCode(), res.Body)
		}
		a := *res.JSON200
		in, vb := eval.Bucket(e.InclusionSalt, unit), eval.Bucket(e.VariationSalt, unit)
		if a.Experiment != "split" || a.UnitType != "user" || a.UnitId != unit || a.InclusionSalt != e.InclusionSalt || a.VariationSalt != e.VariationSalt ||
			a.InclusionBucket != in || a.VariationBucket != vb || a.TrafficAllocation != 6000 || a.Included != (in < 6000) {
			t.Fatalf("assignment of %s = %+v", unit, a)
		}
		want := "a"
		if vb >= 3000 {
			want = "b"
		}
		switch {
		case !a.Included && a.Variation != nil:
			t.Errorf("%s left out but assigned %s", unit, *a.Variation)
		case a.Included && (a.Variation == nil || *a.Variation != want):
			t.Errorf("%s assigned %v, want %s", unit, a.Variation, want)
		case a.Included:
			seen[want] = true
		default:
			seen["left out"] = true
		}
		if !strings.Contains(a.Sql, "murmurHash3_32(concat('"+e.InclusionSalt+"', '.', unit_id)) % 10000 AS inclusion_bucket") ||
			!strings.Contains(a.Sql, "SELECT '"+unit+"' AS unit_id") {
			t.Fatalf("SQL of %s =\n%s", unit, a.Sql)
		}
	}
	if len(seen) != 3 {
		t.Errorf("200 units met only %v", seen)
	}
	if after := auditActions(t, f.admin, f.project.Key); !slices.Equal(after, audit) {
		t.Errorf("looking assignments up recorded %v", after[:len(after)-len(audit)])
	}
}

func TestAnAssignmentIsAnsweredInEveryStatus(t *testing.T) {
	f := startExperiments(t)
	for _, status := range []experiments.Status{experiments.StatusRunning, experiments.StatusStopped, experiments.StatusArchived} {
		key := string(status)
		e := createExperiment(t, f.admin, f.project.Key, validExperiment(key))
		putInStatus(t, f, key, status)
		res := assignment(t, f.admin, f.project.Key, key, "user-1")
		if res.JSON200 == nil || res.JSON200.InclusionBucket != eval.Bucket(e.InclusionSalt, "user-1") {
			t.Errorf("assignment in a %s experiment = %d %s", status, res.StatusCode(), res.Body)
		}
	}
}

func TestAnAssignmentRefusesABadUnitIDAndAnUnknownExperiment(t *testing.T) {
	f := startExperiments(t)
	createExperiment(t, f.admin, f.project.Key, validExperiment("split"))
	for _, unit := range []string{"", strings.Repeat("x", 257)} {
		res := assignment(t, f.admin, f.project.Key, "split", unit)
		assertProblem(t, fmt.Sprintf("unit ID of %d bytes", len(unit)), res.StatusCode(), res.Body, http.StatusBadRequest, "invalid_request")
	}
	if res := assignment(t, f.admin, f.project.Key, "split", strings.Repeat("x", 256)); res.JSON200 == nil {
		t.Errorf("unit ID of 256 bytes = %d %s", res.StatusCode(), res.Body)
	}
	res := assignment(t, f.admin, f.project.Key, "absent", "user-1")
	if res.StatusCode() != http.StatusNotFound {
		t.Errorf("assignment in an unknown experiment = %d %s", res.StatusCode(), res.Body)
	}
}

func hashCheck(t *testing.T, c *management.ClientWithResponses, project string, body management.HashCheckRequest) *management.CheckHashUniformityResponse {
	t.Helper()
	return do(c.CheckHashUniformityWithResponse(t.Context(), project, body))
}

func manyUnitIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("customer-%05d", i)
	}
	return out
}

func TestTheUniformityCheckBinsRealUnitIDs(t *testing.T) {
	f := startExperiments(t)
	viewer := testkit.Client(t, f.srv, testkit.GrantedToken(t, testkit.Pool(t), projectIDOf(f.project), access.RoleViewer))
	audit := auditActions(t, f.admin, f.project.Key)

	res := hashCheck(t, viewer, f.project.Key, management.HashCheckRequest{UnitIds: manyUnitIDs(1000)})
	if res.JSON200 == nil {
		t.Fatalf("POST hash-check = %d %s", res.StatusCode(), res.Body)
	}
	h := *res.JSON200
	total := 0
	for _, c := range h.Counts {
		total += c
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(h.Salt) || h.Bins != 100 || len(h.Counts) != 100 || total != 1000 ||
		h.Df != 99 || h.Min != slices.Min(h.Counts) || h.Max != slices.Max(h.Counts) || h.Independence != nil || h.SecondSalt != nil {
		t.Errorf("default check = %+v", h)
	}

	second := "0f1e2d3c4b5a6978"
	res = hashCheck(t, viewer, f.project.Key, management.HashCheckRequest{UnitIds: manyUnitIDs(20000), Salt: ptr("a1b2c3d4e5f60718"), SecondSalt: &second, Bins: ptr(10)})
	if res.JSON200 == nil {
		t.Fatalf("POST hash-check with two salts = %d %s", res.StatusCode(), res.Body)
	}
	h = *res.JSON200
	if h.Salt != "a1b2c3d4e5f60718" || h.SecondSalt == nil || *h.SecondSalt != second || len(h.Counts) != 10 ||
		h.Verdict != management.HashCheckVerdictUniform || h.Independence == nil ||
		h.Independence.Verdict != management.HashCheckIndependenceVerdictIndependent || h.Independence.Df != 81 {
		t.Errorf("check with two salts = %+v", h)
	}
	want, _ := experiments.CheckUniformity(experiments.UniformityRequest{UnitIDs: manyUnitIDs(20000), Salt: "a1b2c3d4e5f60718", SecondSalt: &second, Bins: 10})
	if !slices.Equal(h.Counts, want.Counts) || h.ChiSquare != want.ChiSquare || h.PValue != want.PValue || h.Independence.ChiSquare != want.Independence.ChiSquare {
		t.Errorf("the API answered %+v, the check computes %+v", h, want)
	}

	if after := auditActions(t, f.admin, f.project.Key); !slices.Equal(after, audit) {
		t.Errorf("checking recorded %v", after[:len(after)-len(audit)])
	}
}

func TestTheUniformityCheckRefusesWhatItCannotCheck(t *testing.T) {
	f := startExperiments(t)
	bad := manyUnitIDs(1000)
	bad[5] = ""
	for _, c := range []struct {
		name    string
		body    management.HashCheckRequest
		pointer string
		code    string
	}{
		{"too few", management.HashCheckRequest{UnitIds: manyUnitIDs(999)}, "/unit_ids", "out_of_range"},
		{"an empty unit ID", management.HashCheckRequest{UnitIds: bad}, "/unit_ids/5", "invalid_value"},
		{"one bin", management.HashCheckRequest{UnitIds: manyUnitIDs(1000), Bins: ptr(1)}, "/bins", "out_of_range"},
		{"an empty salt", management.HashCheckRequest{UnitIds: manyUnitIDs(1000), Salt: ptr("")}, "/salt", "invalid_value"},
		{"a long second salt", management.HashCheckRequest{UnitIds: manyUnitIDs(1000), SecondSalt: ptr(strings.Repeat("s", 257))}, "/second_salt", "invalid_value"},
	} {
		res := hashCheck(t, f.admin, f.project.Key, c.body)
		p := assertProblem(t, c.name, res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_uniformity_check")
		if p.Errors == nil || !slices.ContainsFunc(*p.Errors, func(v management.Violation) bool { return v.Pointer == c.pointer && v.Code == c.code }) {
			t.Errorf("%s: violations = %+v, want %s on %s", c.name, p.Errors, c.code, c.pointer)
		}
	}
	res := hashCheck(t, f.admin, "absent", management.HashCheckRequest{UnitIds: manyUnitIDs(1000)})
	if res.StatusCode() != http.StatusNotFound {
		t.Errorf("check in an unknown project = %d %s", res.StatusCode(), res.Body)
	}
}

// A body of 200,000 unit IDs is far over the server's limit for other
// bodies; the check takes it.
func TestTheUniformityCheckTakesTheLargestList(t *testing.T) {
	f := startExperiments(t)
	ids := manyUnitIDs(200000)
	for i := range ids {
		ids[i] += strings.Repeat("-", 40)
	}
	res := hashCheck(t, f.admin, f.project.Key, management.HashCheckRequest{UnitIds: ids})
	if res.JSON200 == nil || res.JSON200.Max < res.JSON200.Min || len(res.JSON200.Counts) != 100 {
		t.Fatalf("200,000 unit IDs = %d %.300s", res.StatusCode(), res.Body)
	}
	res = hashCheck(t, f.admin, f.project.Key, management.HashCheckRequest{UnitIds: append(ids, "one-more")})
	assertProblem(t, "200,001 unit IDs", res.StatusCode(), res.Body, http.StatusUnprocessableEntity, "invalid_uniformity_check")
}
