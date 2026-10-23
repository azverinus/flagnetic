package experiments_test

import (
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/domain/experiments"
	"github.com/azverinus/flagnetic/internal/domain/flags"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var now = time.Date(2026, time.March, 1, 10, 0, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time { return now }

var (
	ids     = id.NewDeterministicSource(fixedClock{})
	project = id.New[workspace.Project](ids)
	anna    = id.New[access.Member](ids)
	flag    = experiments.FlagFacts{
		ID: id.New[flags.Flag](ids), Key: "checkout_title", Variations: []string{"long", "short", "huge"},
	}
)

func ptr[T any](v T) *T { return &v }

func input() experiments.Input {
	return experiments.Input{
		ID: id.New[experiments.Experiment](ids), Project: project, Key: "checkout_v2", Name: "Checkout v2",
		Owner: experiments.Owner{Member: anna, Eligible: true}, Flag: &flag, FlagKey: "checkout_title",
		UnitType: "user", DeclaredUnitTypes: []string{"user", "anonymous"},
		Variations: []experiments.Weight{{Variation: "long", Weight: 4000}, {Variation: "short", Weight: 6000}},
		Salts:      experiments.Salts{Inclusion: "a3f9c2e17b4d0e58", Variation: "7e1d4c90b2a6f3d1"},
		CreatedBy:  &anna, Now: now,
	}
}

// violations returns "pointer code" for every violation err carries,
// failing unless err refuses the experiment as invalid_experiment.
func violations(t *testing.T, err error) []string {
	t.Helper()
	e, ok := apperrors.From(err)
	if !ok || e.Kind != apperrors.KindInvalid || e.Code != "invalid_experiment" {
		t.Fatalf("err = %v, want invalid_experiment", err)
	}
	var out []string
	for _, v := range e.Violations {
		out = append(out, v.Pointer+" "+v.Code)
	}
	return out
}

func TestANewExperimentIsADraftAtVersionOneWithAllTheTrafficAllocated(t *testing.T) {
	in := input()
	in.Name, in.Hypothesis, in.Tags = "  Checkout v2 ", " Short titles convert. ", []string{" checkout ", "mobile"}
	in.ExpectedEndAt = ptr(time.Date(2026, time.December, 1, 15, 4, 5, 0, time.FixedZone("x", 3*3600)))

	e, err := experiments.New(in)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != experiments.StatusDraft || e.Version != 1 || e.TrafficAllocation != experiments.TotalWeight {
		t.Errorf("status %s, version %d, allocation %d", e.Status, e.Version, e.TrafficAllocation)
	}
	if e.Name != "Checkout v2" || e.Hypothesis != "Short titles convert." || !slices.Equal(e.Tags, []string{"checkout", "mobile"}) {
		t.Errorf("name %q, hypothesis %q, tags %q", e.Name, e.Hypothesis, e.Tags)
	}
	if e.Flag != flag.ID || e.FlagKey != "checkout_title" || e.Owner != anna || e.CreatedBy == nil || *e.CreatedBy != anna {
		t.Errorf("flag %v %q, owner %v", e.Flag, e.FlagKey, e.Owner)
	}
	if want := time.Date(2026, time.December, 1, 0, 0, 0, 0, time.UTC); e.ExpectedEndAt == nil || !e.ExpectedEndAt.Equal(want) {
		t.Errorf("expected end = %v, want the UTC day %v", e.ExpectedEndAt, want)
	}
	if e.InclusionSalt != "a3f9c2e17b4d0e58" || e.VariationSalt != "7e1d4c90b2a6f3d1" {
		t.Errorf("salts = %q, %q", e.InclusionSalt, e.VariationSalt)
	}
	if !slices.Equal(e.Variations, in.Variations) {
		t.Errorf("variations = %v, want the order given %v", e.Variations, in.Variations)
	}
}

func TestAnExperimentIsRefusedWithEveryFieldToFix(t *testing.T) {
	archived := flag
	archived.Archived = true
	weights := func(ws ...experiments.Weight) []experiments.Weight { return ws }
	w := func(v string, n int) experiments.Weight { return experiments.Weight{Variation: v, Weight: n} }
	many := make([]experiments.Weight, 51)
	for i := range many {
		many[i] = w("v"+string(rune('a'+i%26))+string(rune('a'+i/26)), 0)
	}
	many[0].Weight = experiments.TotalWeight

	for _, c := range []struct {
		what   string
		change func(*experiments.Input)
		want   []string
	}{
		{"a key outside the grammar", func(in *experiments.Input) { in.Key = "2fast" }, []string{"/key invalid_key"}},
		{"a blank name", func(in *experiments.Input) { in.Name = "  " }, []string{"/name invalid_name"}},
		{"a hypothesis over 2000 characters", func(in *experiments.Input) { in.Hypothesis = strings.Repeat("x", 2001) },
			[]string{"/hypothesis too_long"}},
		{"an owner who may not own", func(in *experiments.Input) { in.Owner.Eligible = false }, []string{"/owner invalid_owner"}},
		{"a blank and a repeated tag", func(in *experiments.Input) { in.Tags = []string{" ", "a", "a"} },
			[]string{"/tags/0 invalid_value", "/tags/2 duplicate"}},
		{"a flag the project does not have", func(in *experiments.Input) {
			in.Flag = nil
		}, []string{"/flag unknown_flag"}},
		{"an archived flag", func(in *experiments.Input) { in.Flag = &archived }, []string{"/flag flag_archived"}},
		{"an undeclared unit type", func(in *experiments.Input) { in.UnitType = "device" }, []string{"/unit_type undeclared_unit_type"}},
		{"one variation", func(in *experiments.Input) { in.Variations = weights(w("short", 10000)) }, []string{"/variations invalid_count"}},
		{"fifty-one variations", func(in *experiments.Input) { in.Variations = many }, []string{"/variations invalid_count"}},
		{"the same variation twice", func(in *experiments.Input) { in.Variations = weights(w("short", 5000), w("short", 5000)) },
			[]string{"/variations/1/variation duplicate"}},
		{"a variation the flag does not have", func(in *experiments.Input) { in.Variations = weights(w("short", 5000), w("tiny", 5000)) },
			[]string{"/variations/1/variation unknown_variation"}},
		{"a negative weight", func(in *experiments.Input) { in.Variations = weights(w("short", 10000), w("long", -100)) },
			[]string{"/variations/1/weight invalid_weight"}},
		{"a weight over 10000", func(in *experiments.Input) { in.Variations = weights(w("short", 10100), w("long", -100)) },
			[]string{"/variations/0/weight invalid_weight", "/variations/1/weight invalid_weight"}},
		// Without a bound on each weight, these would wrap around to exactly
		// 10,000 and pass as a valid split.
		{"weights overflowing to 10000", func(in *experiments.Input) {
			in.Variations = weights(w("short", math.MaxInt), w("long", math.MaxInt), w("huge", 10002))
		}, []string{"/variations/0/weight invalid_weight", "/variations/1/weight invalid_weight", "/variations/2/weight invalid_weight"}},
		{"weights summing to 9999", func(in *experiments.Input) { in.Variations = weights(w("short", 5000), w("long", 4999)) },
			[]string{"/variations invalid_weights_sum"}},
		{"a traffic allocation over 10000", func(in *experiments.Input) { in.TrafficAllocation = ptr(10001) },
			[]string{"/traffic_allocation out_of_range"}},
		{"a negative traffic allocation", func(in *experiments.Input) { in.TrafficAllocation = ptr(-1) },
			[]string{"/traffic_allocation out_of_range"}},
		{"several at once", func(in *experiments.Input) { in.Name, in.UnitType = "", "device" },
			[]string{"/name invalid_name", "/unit_type undeclared_unit_type"}},
	} {
		in := input()
		c.change(&in)
		_, err := experiments.New(in)
		if got := violations(t, err); !slices.Equal(got, c.want) {
			t.Errorf("%s: violations = %v, want %v", c.what, got, c.want)
		}
	}
}

func TestAWeightOfZeroIsAllowedAndTheAllocationMayBeAnythingFromNoneToAll(t *testing.T) {
	for _, a := range []int{0, 1, experiments.TotalWeight} {
		in := input()
		in.TrafficAllocation = ptr(a)
		in.Variations = []experiments.Weight{{Variation: "long", Weight: 0}, {Variation: "short", Weight: 10000}}
		e, err := experiments.New(in)
		if err != nil || e.TrafficAllocation != a {
			t.Errorf("allocation %d: %+v, %v", a, e.TrafficAllocation, err)
		}
	}
}
