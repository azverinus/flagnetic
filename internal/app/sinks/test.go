package sinks

import (
	"context"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// Tested is what a test of a sink's target found.
type Tested struct {
	// OK is whether the target answered and can take a batch.
	OK bool
	// Failure is why it cannot; nil when OK.
	Failure *sinks.Failure
	// Warnings are what is wrong with the target that does not stop delivery.
	Warnings []string
}

func tested(p Probed) Tested {
	return Tested{OK: p.Failure == nil, Failure: p.Failure, Warnings: p.Warnings}
}

// TestConfigCommand asks for an unsaved configuration to be tested.
type TestConfigCommand struct {
	Project workspace.ProjectID
	Draft   sinks.Draft
}

// TestConfig tests a configuration that is not saved.
type TestConfig struct{ d Deps }

// NewTestConfig returns the handler.
func NewTestConfig(d Deps) *TestConfig { return &TestConfig{d: d} }

// Handle checks the configuration as saving one is checked, then calls its
// target. Nothing is stored, created at the target or audited: a test of a
// configuration that is not saved is advice. A webhook sink has no secret yet,
// so the call is signed with one drawn for it and thrown away.
func (h *TestConfig) Handle(ctx context.Context, cmd TestConfigCommand) (Tested, error) {
	draft, err := h.d.check(ctx, cmd.Project, cmd.Draft)
	if err != nil {
		return Tested{}, err
	}
	secrets := []string{draft.Secret}
	if draft.Kind == sinks.KindWebhook {
		drawn, drawErr := governance.NewWebhookSecret(h.d.Random)
		if drawErr != nil {
			return Tested{}, drawErr
		}
		secrets = []string{drawn.Reveal()}
	}
	probed, err := h.d.probe(ctx, sinks.Sink{Project: cmd.Project, Input: draft.Input}, secrets, false)
	return tested(probed), err
}

// TestCommand asks for the stored sink Sink of Project to be tested, or an
// edit of it.
type TestCommand struct {
	Project workspace.ProjectID
	Sink    sinks.ID
	// Config is the configuration to test in place of the stored one; nil to
	// test the stored one.
	Config *sinks.Config
	// Secret is the secret the edit sent: empty or sinks.Masked takes the
	// stored one.
	Secret  string
	Request governance.Request
}

// Test tests a saved sink.
type Test struct{ d Deps }

// NewTest returns the handler.
func NewTest(d Deps) *Test { return &Test{d: d} }

// Handle calls the target of the stored sink, or of the stored sink with the
// configuration given, whose masked secret is the stored one. Testing the
// stored configuration also prepares the target when the sink may (ClickHouse
// tables) and stores what the check found as the sink's warnings. It records
// sink.tested.
func (h *Test) Handle(ctx context.Context, cmd TestCommand) (Tested, error) {
	s, err := sinkOf(ctx, h.d.Store, cmd.Project, cmd.Sink)
	if err != nil {
		return Tested{}, err
	}
	now := h.d.now()
	probed, err := h.probe(ctx, cmd, s, now)
	if err != nil {
		return Tested{}, err
	}
	result := tested(probed)
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		if txErr := h.d.lockProject(ctx, cmd.Project); txErr != nil {
			return txErr
		}
		if cmd.Config == nil {
			if txErr := h.d.Store.SaveWarnings(ctx, s.ID, result.Warnings); txErr != nil {
				return txErr
			}
		}
		after := sinks.Sink{ID: s.ID, Project: s.Project, Input: sinks.Input{Name: s.Name, Kind: s.Kind}}
		return h.d.recordDoc(ctx, now, cmd.Request, "sink.tested", after, testedDoc(result))
	})
	return result, err
}

// probe calls the target of s, with the edit applied when there is one.
func (h *Test) probe(ctx context.Context, cmd TestCommand, s sinks.Sink, now time.Time) (Probed, error) {
	if cmd.Config == nil {
		secrets, err := openSecrets(h.d.Opener, s, now)
		if err != nil {
			return Probed{}, err
		}
		return h.d.probe(ctx, s, secrets, true)
	}
	draft, err := h.d.check(ctx, cmd.Project, sinks.Draft{
		Input: edited(s, *cmd.Config), Secret: cmd.Secret, KeepsSecret: sinks.KeepsSecret(cmd.Secret), Stored: &s.Config,
	}, s.Environments...)
	if err != nil {
		return Probed{}, err
	}
	var secrets []string
	if draft.KeepsSecret {
		if secrets, err = openSecrets(h.d.Opener, s, now); err != nil {
			return Probed{}, err
		}
	} else {
		secrets = []string{draft.Secret}
	}
	tested := s
	tested.Input = draft.Input
	return h.d.probe(ctx, tested, secrets, false)
}

// edited returns s's input with the configuration replaced whole.
func edited(s sinks.Sink, c sinks.Config) sinks.Input {
	in := s.Input
	in.Config = c
	return in
}

// testedDoc is what the audit log records of a test: whether it passed, and
// the failure by class, status and code.
func testedDoc(t Tested) map[string]any {
	doc := map[string]any{"ok": t.OK, "failure": nil, "warnings": nonNil(t.Warnings)}
	if t.Failure != nil {
		doc["failure"] = map[string]any{"class": string(t.Failure.Class), "status": nilIfZero(t.Failure.Status), "code": nilIfEmpty(t.Failure.Code)}
	}
	return doc
}

func nilIfZero(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
