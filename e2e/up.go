package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
)

// PostgresImage is the PostgreSQL started when no database URL is given.
const PostgresImage = "postgres:17-alpine"

// UpOptions say how to bring up a seeded instance.
type UpOptions struct {
	// Root is the repository the binary is built from; ignored when Binary
	// is set.
	Root string
	// Binary is a flagnetic binary built already; empty, Up builds one into
	// WorkDir.
	Binary string
	// WorkDir holds the built binary.
	WorkDir string
	// DatabaseURL is the PostgreSQL both replicas share; empty, Up starts a
	// throwaway one in Docker.
	DatabaseURL string
	// Logs receives the replicas' and the commands' output; discarded when
	// nil.
	Logs io.Writer
}

// Instance is a seeded pair of replicas on one database.
type Instance struct {
	A, B  *Replica
	State State

	stops []func() error
}

// State is what control commands need of a running instance: the seed and
// the identifiers of its SDK keys. It is written by `up` and read by every
// other command, which act through the management API and never on the
// database.
type State struct {
	Seed Seed                          `json:"seed"`
	Keys map[management.SdkKeyKind]Key `json:"keys"`
}

// Up builds the binary, applies the migrations once, starts two replicas
// on one PostgreSQL, bootstraps the harness's instance admin and seeds the
// project through replica A. The caller stops the instance.
func Up(ctx context.Context, opts UpOptions) (inst *Instance, err error) {
	if opts.Logs == nil {
		opts.Logs = io.Discard
	}
	inst = &Instance{}
	defer func() {
		if err != nil {
			_ = inst.Stop()
			inst = nil
		}
	}()
	if opts.DatabaseURL == "" {
		c, runErr := tcpostgres.Run(ctx, PostgresImage,
			tcpostgres.WithDatabase("flagnetic"),
			tcpostgres.WithUsername("flagnetic"),
			tcpostgres.WithPassword("flagnetic"),
			tcpostgres.BasicWaitStrategies(),
		)
		if runErr != nil {
			return nil, fmt.Errorf("start PostgreSQL (is Docker running? or set DATABASE_URL): %w", runErr)
		}
		inst.stops = append(inst.stops, func() error { return testcontainers.TerminateContainer(c) })
		if opts.DatabaseURL, err = c.ConnectionString(ctx, "sslmode=disable"); err != nil {
			return nil, err
		}
	}
	if opts.Binary == "" {
		if opts.Binary, err = Build(ctx, opts.Root, opts.WorkDir); err != nil {
			return nil, err
		}
	}
	if err = Migrate(ctx, opts.Binary, opts.DatabaseURL, opts.Logs); err != nil {
		return nil, err
	}
	for _, r := range []**Replica{&inst.A, &inst.B} {
		var replica *Replica
		if replica, err = StartReplica(ctx, opts.Binary, opts.DatabaseURL, opts.Logs); err != nil {
			return nil, err
		}
		*r = replica
		inst.stops = append(inst.stops, replica.Stop)
	}
	if err = BootstrapAdmin(ctx, opts.Binary, opts.DatabaseURL, opts.Logs); err != nil {
		return nil, err
	}
	if inst.State, err = SeedInstance(ctx, Replicas{A: inst.A.URL, B: inst.B.URL}, nil); err != nil {
		return nil, err
	}
	return inst, nil
}

// Stop stops the replicas, then the database Up started.
func (i *Instance) Stop() error {
	var errs []error
	for j := len(i.stops) - 1; j >= 0; j-- {
		errs = append(errs, i.stops[j]())
	}
	i.stops = nil
	return errors.Join(errs...)
}

// SeedInstance brings the instance behind replicas into the seed state
// through replica A and returns it. held are the keys of an earlier
// seeding, kept while they are active; nil creates new ones. Seeding is
// idempotent: what exists is found by key, only what differs from the seed
// is written, so seeding a seeded instance with its keys bumps no ruleset
// version and answers the same seed.
func SeedInstance(ctx context.Context, replicas Replicas, held map[management.SdkKeyKind]Key) (State, error) {
	m, err := SignIn(ctx, replicas.A)
	if err != nil {
		return State{}, err
	}
	salt, err := Restore(ctx, m)
	if err != nil {
		return State{}, err
	}
	keys, err := ensureKeys(ctx, m, held)
	if err != nil {
		return State{}, err
	}
	return State{
		Seed: seed(replicas, salt, keys[management.SdkKeyKindServer].Value, keys[management.SdkKeyKindClient].Value),
		Keys: keys,
	}, nil
}

// DefaultStatePath is where `up` writes its state when FLAGNETIC_E2E_STATE
// names no other file.
func DefaultStatePath() string {
	return filepath.Join(os.TempDir(), "flagnetic-e2e", "state.json")
}

// WriteFile writes v as JSON to path atomically, readable by its owner
// alone, since the state and the seed carry SDK keys.
func WriteFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadState reads the state `up` wrote at path.
func ReadState(path string) (State, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the caller names the state file
	if err != nil {
		return State{}, fmt.Errorf("read the state of a running up (is `flagnetic-e2e up` running?): %w", err)
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return State{}, fmt.Errorf("read %s: %w", path, err)
	}
	return s, nil
}
