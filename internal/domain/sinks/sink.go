// Package sinks holds a project's sinks: the standing request to copy its raw
// events to a ClickHouse database, an S3-compatible bucket or a signed
// webhook. This file is the sink itself, what a member chooses of it and how
// an edit applies.
package sinks

import (
	"maps"
	"slices"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// ID identifies a sink.
type ID = id.Of[Sink]

// Kind is which target a sink delivers to.
type Kind string

// The kinds of sink. A sink's kind is fixed when it is created.
const (
	KindClickHouse    Kind = "clickhouse"
	KindObjectStorage Kind = "object_storage"
	KindWebhook       Kind = "webhook"
)

// Kinds are every kind of sink, in the order the API lists them.
var Kinds = []Kind{KindClickHouse, KindObjectStorage, KindWebhook}

// SecretPurpose is what the secret of a sink of kind k is sealed for: the
// ClickHouse password, the object storage secret access key, or the secret
// signing a webhook sink's calls. Each has a key of its own, so a value
// sealed for one never opens as another.
func (k Kind) SecretPurpose() secret.Purpose {
	switch k {
	case KindClickHouse:
		return secret.PurposeSinkClickHousePassword
	case KindObjectStorage:
		return secret.PurposeSinkObjectStorageSecret
	default:
		return secret.PurposeSinkWebhookSecret
	}
}

// secretPointer is where, in a sink as the API writes it, the secret of a
// sink of kind k stands; empty for a kind whose secret the server draws.
func (k Kind) secretPointer() string {
	switch k {
	case KindClickHouse:
		return "/clickhouse/password"
	case KindObjectStorage:
		return "/object_storage/secret_access_key"
	default:
		return ""
	}
}

// EventKind is a kind of raw event a sink exports; each is delivered through
// a stream of its own.
type EventKind string

// The event kinds, in the order the API lists them.
const (
	EventExposure          EventKind = "exposure"
	EventAlias             EventKind = "alias"
	EventEvaluationSummary EventKind = "evaluation_summary"
)

// EventKinds are every event kind a sink may export.
var EventKinds = []EventKind{EventExposure, EventAlias, EventEvaluationSummary}

// Masked stands for a stored secret wherever a sink is read, and is what a
// client sends back to keep the stored one.
const Masked = "***"

// KeepsSecret reports whether a secret a client sent leaves the stored one
// as it is: it is absent or the mask every read answers.
func KeepsSecret(sent string) bool { return sent == "" || sent == Masked }

// Sink is a project's sink.
type Sink struct {
	ID ID
	// Project is the project whose events the sink exports.
	Project workspace.ProjectID
	Input
	// SealedSecret is the secret of the sink's kind sealed under the
	// instance's secret key: the ClickHouse password, the object storage
	// secret access key, or the secret signing a webhook sink's calls.
	SealedSecret string
	// PreviousSealedSecret is the secret a rotation replaced, which signs
	// too until PreviousSecretExpiresAt; empty outside a rotation's overlap
	// and for the kinds whose secret the target holds.
	PreviousSealedSecret    string
	PreviousSecretExpiresAt *time.Time
	// StoppedAt is when delivery stopped itself after the target kept
	// rejecting batches; nil unless it did.
	StoppedAt *time.Time
	// AtRiskSince is when the sink fell so far behind that retention would
	// drop events it has not delivered; nil unless it did.
	AtRiskSince *time.Time
	// Warnings are what the target's last check found wrong without making
	// delivery impossible, such as a column missing from a table.
	Warnings []string
	// CreatedBy is the member or the project token that created the sink;
	// nil when neither did.
	CreatedBy *governance.Author
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int64
}

// Input is what a member chooses of a sink.
type Input struct {
	Name string
	Kind Kind
	// Config holds exactly the configuration of Kind.
	Config Config
	// EventKinds are the kinds of event exported; none means all of them.
	EventKinds []EventKind
	// Environments are the keys of the environments whose events are
	// exported; none means every one.
	Environments []string
	// BatchSize is the most events one delivery carries.
	BatchSize int
	// FlushInterval is how long the sink waits between deliveries once it
	// has caught up.
	FlushInterval time.Duration
	Enabled       bool
}

// StreamKinds returns the event kinds a sink with these inputs exports, all
// of them when it names none.
func (in Input) StreamKinds() []EventKind {
	if len(in.EventKinds) == 0 {
		return slices.Clone(EventKinds)
	}
	return slices.Clone(in.EventKinds)
}

// Draft is an input with the secret it comes with, which is plaintext until
// it is sealed and is never part of a stored input.
type Draft struct {
	Input
	// Secret is the ClickHouse password or the object storage secret access
	// key a member sent. A webhook sink has none: the server draws it.
	Secret string
	// KeepsSecret is set for an edit that sent none or the mask, so that
	// the stored secret stands and Secret is not looked at.
	KeepsSecret bool
}

// Config is the configuration of a sink: exactly the one of its kind is set.
type Config struct {
	ClickHouse    *ClickHouse
	ObjectStorage *ObjectStorage
	Webhook       *Webhook
}

// ClickHouse is the configuration of a ClickHouse sink. Its password is the
// sink's secret.
type ClickHouse struct {
	// URL is where the HTTP interface answers, http or https, without
	// credentials.
	URL      string
	Database string
	// TablePrefix prefixes the three tables the sink writes.
	TablePrefix   string
	Username      string
	TLSSkipVerify bool
	// CreateTables lets the platform create its tables when it enables the
	// sink.
	CreateTables bool
	// Settings are ClickHouse settings sent with every insert.
	Settings map[string]string
}

// ObjectStorage is the configuration of an object storage sink. Its secret
// access key is the sink's secret.
type ObjectStorage struct {
	// Endpoint is the scheme, host and port of an S3-compatible service;
	// empty for AWS.
	Endpoint string
	// Region is asked of the bucket when empty.
	Region      string
	Bucket      string
	Prefix      string
	AccessKeyID string
	PathStyle   bool
	// Format is "jsonl" or "parquet".
	Format string
	// Compression is "gzip", "zstd" or "none" for jsonl, and "zstd" for
	// parquet.
	Compression string
}

// Webhook is the configuration of a webhook sink. The secret signing its
// calls is the sink's secret.
type Webhook struct {
	URL string
	// Headers are sent with every call, before the platform's own.
	Headers map[string]string
}

// Clone returns c sharing nothing with it.
func (c Config) Clone() Config {
	if c.ClickHouse != nil {
		v := *c.ClickHouse
		v.Settings = maps.Clone(v.Settings)
		c.ClickHouse = &v
	}
	if c.ObjectStorage != nil {
		v := *c.ObjectStorage
		c.ObjectStorage = &v
	}
	if c.Webhook != nil {
		v := *c.Webhook
		v.Headers = maps.Clone(v.Headers)
		c.Webhook = &v
	}
	return c
}

// Is reports whether c sets the configuration of kind, and no other.
func (c Config) Is(kind Kind) bool {
	return (c.ClickHouse != nil) == (kind == KindClickHouse) &&
		(c.ObjectStorage != nil) == (kind == KindObjectStorage) &&
		(c.Webhook != nil) == (kind == KindWebhook)
}

// TargetURL returns the URL the configuration reaches, empty for AWS.
func (c Config) TargetURL() string {
	switch {
	case c.ClickHouse != nil:
		return c.ClickHouse.URL
	case c.ObjectStorage != nil:
		return c.ObjectStorage.Endpoint
	case c.Webhook != nil:
		return c.Webhook.URL
	}
	return ""
}

// Equal reports whether a and b are the same configuration.
func (c Config) Equal(other Config) bool {
	switch {
	case c.ClickHouse != nil && other.ClickHouse != nil:
		a, b := *c.ClickHouse, *other.ClickHouse
		return maps.Equal(a.Settings, b.Settings) && a.URL == b.URL && a.Database == b.Database &&
			a.TablePrefix == b.TablePrefix && a.Username == b.Username && a.TLSSkipVerify == b.TLSSkipVerify &&
			a.CreateTables == b.CreateTables
	case c.ObjectStorage != nil && other.ObjectStorage != nil:
		return *c.ObjectStorage == *other.ObjectStorage
	case c.Webhook != nil && other.Webhook != nil:
		return c.Webhook.URL == other.Webhook.URL && maps.Equal(c.Webhook.Headers, other.Webhook.Headers)
	}
	return false
}

// Same reports whether in and other choose the same sink.
func (in Input) Same(other Input) bool {
	return in.Name == other.Name && in.Kind == other.Kind && in.Config.Equal(other.Config) &&
		slices.Equal(in.EventKinds, other.EventKinds) && slices.Equal(in.Environments, other.Environments) &&
		in.BatchSize == other.BatchSize && in.FlushInterval == other.FlushInterval && in.Enabled == other.Enabled
}

// Change is an edit of a sink; a nil field is left as it is. A Config
// replaces the sink's whole configuration, so nothing of the earlier one
// lingers.
type Change struct {
	Name *string
	// Kind, when set, must be the sink's own: a kind never changes.
	Kind          *Kind
	Config        *Config
	EventKinds    *[]EventKind
	Environments  *[]string
	BatchSize     *int
	FlushInterval *time.Duration
	Enabled       *bool
}

// RotateSecret returns s signing under sealed from now, the secret it
// replaces signing beside it for overlap so a receiver can take the new one
// without losing an event. Only two secrets sign at a time: one an earlier
// rotation left overlapping is dropped.
func (s Sink) RotateSecret(sealed string, now time.Time, overlap time.Duration) Sink {
	expires := now.Add(overlap)
	s.PreviousSealedSecret, s.PreviousSecretExpiresAt, s.SealedSecret = s.SealedSecret, &expires, sealed
	s.UpdatedAt, s.Version = now, s.Version+1
	return s
}
