// Package secret holds values that must never reach a log: passwords, keys,
// tokens. Every way of printing one (fmt, slog, JSON) writes [redacted]; only
// an explicit Reveal gives the value, at the one place that needs it.
package secret

import "log/slog"

const redacted = "[redacted]"

// String is a secret string. Its zero value is the empty secret.
type String struct {
	v string
}

// New wraps v.
func New(v string) String { return String{v: v} }

// Reveal returns the value. Call it only where the value is used, never to log.
func (s String) Reveal() string { return s.v }

// IsZero reports whether the secret is empty.
func (s String) IsZero() bool { return s.v == "" }

// String returns [redacted].
func (String) String() string { return redacted }

// GoString returns [redacted], for %#v.
func (String) GoString() string { return redacted }

// LogValue returns [redacted].
func (String) LogValue() slog.Value { return slog.StringValue(redacted) }

// MarshalText returns [redacted], so an encoded configuration hides it too.
func (String) MarshalText() ([]byte, error) { return []byte(redacted), nil }
