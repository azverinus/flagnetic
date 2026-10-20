package flagnetic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"

	"github.com/azverinus/flagnetic/eval"
)

// Flag types as a ruleset names them.
const (
	typeBoolean = "boolean"
	typeString  = "string"
	typeNumber  = "number"
	typeJSON    = "json"
	// typeInt64 is a number flag read through Int, which serves only
	// values that are whole and fit in an int64.
	typeInt64 = "int64"
)

// Every getter takes a context.Context first, as every Go call on a request
// path does, so that tracing can follow an evaluation without a change of
// signature; then the flag context fc, the flag key and the built-in
// default. Every getter is total: it never panics and never fails, and
// serves def with reason fallback when it cannot serve the flag.

// Bool serves a boolean flag for fc, or def with reason fallback.
func (c *Client) Bool(ctx context.Context, fc Context, key string, def bool) bool {
	v, _ := c.evaluate(ctx, fc, key, typeBoolean, def).Value.(bool)
	return v
}

// String serves a string flag for fc, or def with reason fallback.
func (c *Client) String(ctx context.Context, fc Context, key string, def string) string {
	v, _ := c.evaluate(ctx, fc, key, typeString, def).Value.(string)
	return v
}

// Number serves a number flag for fc, or def with reason fallback.
func (c *Client) Number(ctx context.Context, fc Context, key string, def float64) float64 {
	v, _ := c.evaluate(ctx, fc, key, typeNumber, def).Value.(float64)
	return v
}

// Int serves a number flag for fc as an int64, or def with reason fallback.
// A served number that is not whole or does not fit in an int64 serves def
// with error type_mismatch rather than a truncated value.
func (c *Client) Int(ctx context.Context, fc Context, key string, def int64) int64 {
	v, _ := c.evaluate(ctx, fc, key, typeInt64, def).Value.(int64)
	return v
}

// JSON serves a JSON flag for fc as raw JSON, or def with reason fallback.
// The returned bytes are the caller's to keep or modify.
func (c *Client) JSON(ctx context.Context, fc Context, key string, def json.RawMessage) json.RawMessage {
	v, _ := c.evaluate(ctx, fc, key, typeJSON, def).Value.(json.RawMessage)
	return v
}

// Detail serves a flag for fc with why it was served. The Go type of def
// names the flag type asked for: bool is boolean, string is string, any Go
// number is number, anything else is JSON. A served number is a float64 and
// a served JSON value a json.RawMessage; with reason fallback, Value is def
// as passed. An int64 default reads the flag as Int does: Value is an int64,
// and a number that is not whole or does not fit serves def with
// type_mismatch.
func (c *Client) Detail(ctx context.Context, fc Context, key string, def any) Detail {
	typ := requestedType(def)
	if _, ok := def.(int64); ok {
		typ = typeInt64
	}
	return c.evaluate(ctx, fc, key, typ, def)
}

// flagTypeOf is the flag type a requested type reads.
func flagTypeOf(requested string) string {
	if requested == typeInt64 {
		return typeNumber
	}
	return requested
}

// asInt64 is n as an int64 when n is whole and in range. 2^63 is exactly
// representable as a float64 but one past the largest int64.
func asInt64(n float64) (int64, bool) {
	if n != math.Trunc(n) || n < -(1<<63) || n >= 1<<63 {
		return 0, false
	}
	return int64(n), true
}

func knownType(typ string) bool {
	switch typ {
	case typeBoolean, typeString, typeNumber, typeJSON:
		return true
	}
	return false
}

func requestedType(def any) string {
	switch def.(type) {
	case bool:
		return typeBoolean
	case string:
		return typeString
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr:
		return typeNumber
	default:
		return typeJSON
	}
}

// evaluate is the API boundary every getter goes through: it never panics,
// it counts what it served in the recorder, and it hands the result to the
// exposure queue, which remembers it for a later Exposure call and records
// one at once for a flag with automatic_exposure.
// ctx is not read yet; it is the place tracing will attach to.
func (c *Client) evaluate(_ context.Context, fc Context, key, typ string, def any) (d Detail) {
	if c == nil {
		return Detail{Value: def, Reason: ReasonFallback, ErrorCode: ErrorNoRuleset, Stage: StageBuiltIn}
	}
	automatic := false
	defer func() {
		if r := recover(); r != nil {
			c.log(slog.LevelError, EventEvaluationFailed, map[string]any{"flag": key, "cause": fmt.Sprint(r)})
			d, automatic = c.fallback(c.ruleset.Load(), def, ErrorGeneral), false
		}
		c.count(key, d)
		c.exposures.evaluated(fc, key, d, automatic)
	}()
	d, automatic = c.detail(fc, key, typ, def)
	return d
}

// count records what was served: the flag, the variation and the
// evaluation reason, for the evaluation summary of this interval and, for
// an offline client, for its Recorder. Neither ever holds a unit ID, an
// attribute or a value.
func (c *Client) count(key string, d Detail) {
	c.recorder.count(key, d)
	c.summaries.count(key, d)
}

// detail serves the flag and reports whether the flag is opted into
// automatic exposure. A local override, a missing flag and every fallback
// are never an exposure, so they report false whatever the flag says.
func (c *Client) detail(fc Context, key, typ string, def any) (Detail, bool) {
	loaded := c.ruleset.Load()
	flagType := flagTypeOf(typ)
	if o, ok := c.localOverride(loaded, key); ok {
		if o.typ != flagType {
			c.mismatch(key, o.typ, typ)
			return c.fallback(loaded, def, ErrorTypeMismatch), false
		}
		value, ok := c.asRequested(key, typ, o.served())
		if !ok {
			return c.fallback(loaded, def, ErrorTypeMismatch), false
		}
		d := c.base(loaded)
		d.Value = value
		d.Reason = ReasonLocalOverride
		return d, false
	}
	if loaded == nil {
		return c.fallback(nil, def, ErrorNoRuleset), false
	}
	f, ok := loaded.rs.Flag(key)
	if !ok || !knownType(f.Type) {
		// A flag of a type this SDK does not know is absent, so a server
		// that adds a flag type never breaks an older SDK's other flags.
		return c.fallback(loaded, def, ErrorFlagNotFound), false
	}
	if f.Type != flagType {
		c.mismatch(key, f.Type, typ)
		return c.fallback(loaded, def, ErrorTypeMismatch), false
	}
	r := loaded.rs.Evaluate(fc.eval(), key)
	if r.Reason == eval.ReasonError {
		return c.fallback(loaded, def, ErrorCode(r.ErrorCode)), false
	}
	value, ok := decodeValue(flagType, r.Value)
	if !ok {
		return c.fallback(loaded, def, ErrorInconsistentRuleset), false
	}
	if value, ok = c.asRequested(key, typ, value); !ok {
		return c.fallback(loaded, def, ErrorTypeMismatch), false
	}
	d := c.base(loaded)
	d.Value = value
	d.Reason = Reason(r.Reason)
	d.Variation = r.Variation
	d.RuleID = r.RuleID
	d.Overridden = r.OverrideInfluenced
	d.Experiment = experimentOf(r.Experiment)
	return d, r.AutomaticExposure
}

// asRequested converts a served value of the flag's type to the Go type
// the getter returns. Only Int converts: a number that is not whole or does
// not fit in an int64 is a type mismatch, logged once per flag, rather than
// a silently truncated value.
func (c *Client) asRequested(key, typ string, value any) (any, bool) {
	if typ != typeInt64 {
		return value, true
	}
	n, _ := value.(float64)
	i, ok := asInt64(n)
	if !ok {
		c.mismatch(key, typeNumber, typ)
		return nil, false
	}
	return i, true
}

// base is a Detail carrying the stage and the ruleset it was evaluated
// against.
func (c *Client) base(loaded *loadedRuleset) Detail {
	d := Detail{Stage: c.Stage()}
	if loaded != nil {
		d.RulesetVersion = loaded.rs.Version
		d.ReceivedAt = loaded.receivedAt
	}
	return d
}

func (c *Client) fallback(loaded *loadedRuleset, def any, code ErrorCode) Detail {
	d := c.base(loaded)
	d.Value = def
	d.Reason = ReasonFallback
	d.ErrorCode = code
	return d
}

// mismatch logs type_mismatch once per flag key.
func (c *Client) mismatch(key, flagType, requested string) {
	if _, logged := c.mismatched.LoadOrStore(key, struct{}{}); logged {
		return
	}
	c.log(slog.LevelWarn, EventTypeMismatch, map[string]any{
		"flag":           key,
		"flag_type":      flagType,
		"requested_type": requested,
	})
}

// decodeValue turns a served variation value into the Go value of its flag
// type. A value that does not fit the type is a defect of the ruleset.
func decodeValue(typ string, raw json.RawMessage) (any, bool) {
	if typ != typeJSON && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false // json.Unmarshal accepts null into any Go type
	}
	switch typ {
	case typeBoolean:
		var v bool
		return v, json.Unmarshal(raw, &v) == nil
	case typeString:
		var v string
		return v, json.Unmarshal(raw, &v) == nil
	case typeNumber:
		var v float64
		return v, json.Unmarshal(raw, &v) == nil
	default:
		// Compacting also copies: the ruleset is shared and must never be
		// mutated through a served value.
		var buf bytes.Buffer
		if err := json.Compact(&buf, raw); err != nil {
			return nil, false
		}
		return json.RawMessage(buf.Bytes()), true
	}
}
