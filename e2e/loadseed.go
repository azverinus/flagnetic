package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

// What the load generator needs of the seeded project besides the harness's
// own flags: a project of many flags to evaluate and serve as a ruleset, and
// a running experiment that exposures can name.
const (
	// LoadFlags is the number of flags a load run seeds by default.
	LoadFlags = 1000

	loadFlagPrefix = "load-flag-"
	// LoadExperimentFlag is the flag of the running experiment
	// LoadExperiment, whose variations are control and treatment.
	LoadExperimentFlag = "load-experiment-flag"
	LoadExperiment     = "load-experiment"

	loadWorkers = 8
)

// LoadFlagKey is the key of the i-th flag a load run seeds.
func LoadFlagKey(i int) string { return fmt.Sprintf("%s%04d", loadFlagPrefix, i) }

// loadFlag is flag i: a boolean that is on, serves true to five unit IDs by
// a rule and false to every other, so that evaluating it takes a rule match
// like a real flag does.
func loadFlag(i int) seedFlag {
	ids := make([]any, 0, 5)
	for j := range 5 {
		ids = append(ids, "user-"+strconv.Itoa(i*5+j))
	}
	conditions := []management.Condition{{Kind: "attribute", Attribute: ptr(UnitType), Op: ptr("in"), Value: ids}}
	return seedFlag{
		create: management.FlagCreate{Key: LoadFlagKey(i), Type: management.FlagTypeBoolean, Name: fmt.Sprintf("Load flag %04d", i)},
		configuration: func(current management.FlagConfiguration) management.FlagConfigurationPut {
			rule := management.RuleInput{
				Id: ruleID(current, "variation"), Conditions: &conditions,
				Serve: management.Serve{Kind: "variation", Variation: ptr("true")},
			}
			return management.FlagConfigurationPut{On: true, OffVariation: "false", Fallthrough: "false", Rules: &[]management.RuleInput{rule}}
		},
	}
}

// SeedLoad adds n flags and a running experiment to the seeded project
// through replica A. It is idempotent: what exists is found by key, so a
// second run writes nothing and bumps no ruleset version.
func SeedLoad(ctx context.Context, replicas Replicas, n int) error {
	m, err := SignIn(ctx, replicas.A)
	if err != nil {
		return err
	}
	experimentFlag := seedFlag{
		create: management.FlagCreate{Key: LoadExperimentFlag, Type: management.FlagTypeString, Name: "Load experiment", Variations: variations("control", "treatment")},
		configuration: func(management.FlagConfiguration) management.FlagConfigurationPut {
			return management.FlagConfigurationPut{On: true, OffVariation: "control", Fallthrough: "control"}
		},
	}
	if err := ensureConfiguredFlag(ctx, m, experimentFlag); err != nil {
		return err
	}
	if err := ensureRunningExperiment(ctx, m); err != nil {
		return err
	}

	jobs := make(chan int)
	errs := make(chan error, loadWorkers)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	for range loadWorkers {
		wg.Go(func() {
			for i := range jobs {
				if err := ensureConfiguredFlag(ctx, m, loadFlag(i)); err != nil {
					errs <- err
					cancel()
					return
				}
			}
		})
	}
feed:
	for i := range n {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	close(errs)
	return <-errs
}

func ensureConfiguredFlag(ctx context.Context, m *Management, f seedFlag) error {
	if err := ensureFlag(ctx, m, f.create); err != nil {
		return err
	}
	current, err := configuration(ctx, m, f.create.Key)
	if err != nil {
		return err
	}
	want := f.configuration(current)
	if configurationIs(current, want) {
		return nil
	}
	_, err = putConfiguration(ctx, m, f.create.Key, current.Version, want)
	return err
}

func ensureRunningExperiment(ctx context.Context, m *Management) error {
	res, err := m.GetExperimentWithResponse(ctx, ProjectKey, LoadExperiment)
	if err != nil {
		return err
	}
	switch {
	case res.JSON200 != nil:
		if res.JSON200.Status == management.ExperimentStatusRunning {
			return nil
		}
		return fmt.Errorf("experiment %s is %s, not running", LoadExperiment, res.JSON200.Status)
	case res.StatusCode() != http.StatusNotFound:
		return unexpected("read experiment "+LoadExperiment, res.StatusCode(), res.Body)
	}
	created, err := m.CreateExperimentWithResponse(ctx, ProjectKey, management.ExperimentCreate{
		Key: LoadExperiment, Name: "Load experiment", Flag: LoadExperimentFlag, UnitType: UnitType,
		Hypothesis:    ptr("Exposures are ingested at the rate the load run sends them."),
		ExpectedEndAt: &openapi_types.Date{Time: time.Now().AddDate(1, 0, 0)},
		Variations:    []management.ExperimentWeight{{Variation: "control", Weight: 5000}, {Variation: "treatment", Weight: 5000}},
	})
	if err != nil {
		return err
	}
	if created.JSON201 == nil {
		return unexpected("create experiment "+LoadExperiment, created.StatusCode(), created.Body)
	}
	started, err := m.StartExperimentWithResponse(ctx, ProjectKey, LoadExperiment,
		&management.StartExperimentParams{IfMatch: ptr(strconv.Quote(strconv.FormatInt(created.JSON201.Version, 10)))})
	if err != nil {
		return err
	}
	if started.StatusCode() != http.StatusOK {
		return unexpected("start experiment "+LoadExperiment, started.StatusCode(), started.Body)
	}
	return nil
}

// ToggleLoadFlag switches the load flag i on or off through replica A and
// returns when the change was committed: the time a ruleset stream's
// subscribers are measured from.
func ToggleLoadFlag(ctx context.Context, m *Management, i int, on bool) (time.Time, error) {
	key := LoadFlagKey(i)
	current, err := configuration(ctx, m, key)
	if err != nil {
		return time.Time{}, err
	}
	want := loadFlag(i).configuration(current)
	want.On = on
	updated, err := putConfiguration(ctx, m, key, current.Version, want)
	if err != nil {
		return time.Time{}, err
	}
	return updated.UpdatedAt, nil
}
