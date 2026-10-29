// Package sinks holds the use cases of a project's sinks: the resource a
// maintainer creates, reads, edits and deletes, whose credentials are
// sealed and never read back.
package sinks

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// ErrNotFound is what a store answers for a row it does not have.
var ErrNotFound = errors.New("sinks: not found")

// Transactor runs a use case's reads and writes as one unit of work.
type Transactor interface {
	Within(ctx context.Context, fn func(ctx context.Context) error) error
}

// AuditLog records the audit entry of a change, in the unit of work of the
// change.
type AuditLog interface {
	Record(ctx context.Context, e governance.Entry) error
}

// Store keeps a project's sinks.
type Store interface {
	// LockProject locks project's row for the unit of work, which every
	// write of the project's sinks takes first, or answers ErrNotFound when
	// there is none.
	LockProject(ctx context.Context, project workspace.ProjectID) error
	// EnvironmentKeys returns the keys of project's environments.
	EnvironmentKeys(ctx context.Context, project workspace.ProjectID) ([]string, error)
	// Sinks returns project's sinks, the oldest first.
	Sinks(ctx context.Context, project workspace.ProjectID) ([]sinks.Sink, error)
	// Sink returns project's sink with sink, or ErrNotFound.
	Sink(ctx context.Context, project workspace.ProjectID, sink sinks.ID) (sinks.Sink, error)
	Create(ctx context.Context, s sinks.Sink) error
	// Update stores what an edit changes of s: its input, secret, update
	// time and version, read under the project's lock.
	Update(ctx context.Context, s sinks.Sink) error
	// Delete deletes project's sink with sink, read under the project's
	// lock.
	Delete(ctx context.Context, project workspace.ProjectID, sink sinks.ID) error
	// RotateSecret stores s's secrets and version, read under the project's
	// lock.
	RotateSecret(ctx context.Context, s sinks.Sink) error
}

// Resolver resolves a target's host name to its addresses.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Sealer seals a value under the instance's secret key.
type Sealer interface {
	Seal(purpose secret.Purpose, plaintext []byte) (string, error)
}

// Deps are what the sink use cases run on.
type Deps struct {
	Tx       Transactor
	Store    Store
	Audit    AuditLog
	Clock    clock.Clock
	IDs      *id.Source
	Random   io.Reader
	Sealer   Sealer
	Resolver Resolver
	// Targets is what the operator allows a webhook sink's target to be.
	Targets governance.TargetPolicy
	// SecretOverlap is how long a rotated webhook secret signs beside the
	// new one.
	SecretOverlap time.Duration
}

// resolveTimeout bounds how long a write waits for a target's name to
// resolve.
const resolveTimeout = 5 * time.Second

func (d Deps) now() time.Time { return d.Clock.Now().UTC().Truncate(time.Microsecond) }

var (
	errProjectNotFound = apperrors.NotFound("not_found", "no project has this key")
	// ErrSinkNotFound answers a sink the project does not have.
	ErrSinkNotFound = apperrors.NotFound("not_found", "the project has no sink with this identifier")
	// errNoSecretToRotate refuses to rotate the secret of a sink whose
	// secret the target holds.
	errNoSecretToRotate = apperrors.Invalid("no_secret_to_rotate",
		"only a webhook sink has a secret the server draws; a ClickHouse password and an object storage "+
			"secret access key are changed by editing the sink")
)

// sinkOf returns project's sink, or not_found.
func sinkOf(ctx context.Context, store Store, project workspace.ProjectID, sink sinks.ID) (sinks.Sink, error) {
	s, err := store.Sink(ctx, project, sink)
	if errors.Is(err, ErrNotFound) {
		return s, ErrSinkNotFound
	}
	return s, err
}

// lockProject takes the lock project's sinks are written under, answering a
// project deleted since the request found it as unknown.
func (d Deps) lockProject(ctx context.Context, project workspace.ProjectID) error {
	err := d.Store.LockProject(ctx, project)
	if errors.Is(err, ErrNotFound) {
		return errProjectNotFound
	}
	return err
}

// check returns the draft as sinks.Check accepts it against project's
// environments as the store reads them, plus kept: the keys a sink already
// holds and the edit leaves alone. An environment deleted after the sink
// named it would otherwise refuse every later edit, a rename or a disable
// included, until the caller rewrote the environments too.
func (d Deps) check(ctx context.Context, project workspace.ProjectID, draft sinks.Draft, kept ...string) (sinks.Draft, error) {
	environments, err := d.Store.EnvironmentKeys(ctx, project)
	if err != nil {
		return draft, err
	}
	return sinks.Check(draft, append(environments, kept...), d.Targets)
}

// keptEnvironments returns the environments of s that change leaves as they
// are: all of them when it names none, and otherwise none.
func keptEnvironments(s sinks.Sink, change sinks.Change) []string {
	if change.Environments != nil {
		return nil
	}
	return s.Environments
}

// checkTarget refuses a target whose name resolves to an address the kind
// of sink does not reach: a webhook sink's, one outside the public internet
// unless the operator allows it; any other's, a link-local one. It runs
// before the write's transaction, since it waits on the network. A name that
// does not resolve is accepted: the target may not be up yet, or the
// resolver briefly unreachable. A sink with no URL of its own, AWS's, has
// nothing to check.
func (d Deps) checkTarget(ctx context.Context, kind sinks.Kind, config sinks.Config) error {
	raw := config.TargetURL()
	if raw == "" {
		return nil
	}
	webhook := kind == sinks.KindWebhook
	if webhook && d.Targets.AllowPrivateNetworks {
		return nil
	}
	check := func(addrs []netip.Addr) error {
		if webhook {
			return webhookAddresses(addrs, d.Targets)
		}
		return sinks.CheckNotLinkLocal(addrs, sinks.TargetPointer(kind))
	}
	host := governance.TargetHost(raw)
	if addr, err := netip.ParseAddr(host); err == nil {
		return check([]netip.Addr{addr})
	}
	ctx, cancel := context.WithTimeout(ctx, resolveTimeout)
	defer cancel()
	addrs, _ := d.Resolver.LookupNetIP(ctx, "ip", host)
	return check(addrs)
}

// webhookAddresses refuses a webhook sink's target as a webhook
// subscription's is refused, naming the sink's field.
func webhookAddresses(addrs []netip.Addr, policy governance.TargetPolicy) error {
	if err := governance.CheckTargetAddresses(addrs, policy); err != nil {
		var e *apperrors.Error
		if errors.As(err, &e) {
			return e.Under("/webhook")
		}
		return err
	}
	return nil
}

// redacted stands for a secret in a sink's audit values, so an auditor sees
// there is one and never reads it.
const redacted = "[redacted]"

// values returns what the audit log records of s: its configuration with
// every secret redacted, the values of its headers included, which may carry
// a token.
func values(s sinks.Sink) map[string]any {
	out := map[string]any{
		"id": s.ID.String(), "name": s.Name, "kind": string(s.Kind), "event_kinds": kindNames(s.EventKinds),
		"environments": nonNil(s.Environments), "batch_size": s.BatchSize,
		"flush_interval": int(s.FlushInterval / time.Second), "enabled": s.Enabled,
	}
	switch c := s.Config; {
	case c.ClickHouse != nil:
		settings := map[string]any{}
		for k, v := range c.ClickHouse.Settings {
			settings[k] = v
		}
		out["clickhouse"] = map[string]any{
			"url": c.ClickHouse.URL, "database": c.ClickHouse.Database, "table_prefix": c.ClickHouse.TablePrefix,
			"username": c.ClickHouse.Username, "password": redacted, "tls_skip_verify": c.ClickHouse.TLSSkipVerify,
			"create_tables": c.ClickHouse.CreateTables, "settings": settings,
		}
	case c.ObjectStorage != nil:
		o := c.ObjectStorage
		out["object_storage"] = map[string]any{
			"endpoint": o.Endpoint, "region": o.Region, "bucket": o.Bucket, "prefix": o.Prefix,
			"access_key_id": o.AccessKeyID, "secret_access_key": redacted, "path_style": o.PathStyle,
			"format": o.Format, "compression": o.Compression,
		}
	case c.Webhook != nil:
		headers := map[string]any{}
		for k := range c.Webhook.Headers {
			headers[k] = redacted
		}
		out["webhook"] = map[string]any{"url": c.Webhook.URL, "headers": headers, "secret": redacted}
	}
	return out
}

func kindNames(kinds []sinks.EventKind) []string {
	out := make([]string, len(kinds))
	for i, k := range kinds {
		out[i] = string(k)
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// record writes the audit entry of action on a sink, made at now in req,
// with its values around the change.
func (d Deps) record(ctx context.Context, now time.Time, req governance.Request, action string, before, after *sinks.Sink) error {
	s := after
	if s == nil {
		s = before
	}
	r := governance.Record{
		Project: s.Project, Action: action, Entity: governance.Entity{Kind: "sink", ID: s.ID.String(), Key: s.Name},
	}
	if before != nil {
		r.Before = values(*before)
	}
	if after != nil {
		r.After = values(*after)
	}
	entry, err := governance.NewEntry(id.New[governance.Entry](d.IDs), now, req, r)
	if err != nil {
		return err
	}
	return d.Audit.Record(ctx, entry)
}
