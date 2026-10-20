package flagnetic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"sort"

	"github.com/azverinus/flagnetic/eval"
)

var (
	// ErrLocalOverridesNotAllowed is returned by LocalOverrides.Set on a client
	// constructed without WithLocalOverrides.
	ErrLocalOverridesNotAllowed = errors.New("flagnetic: local overrides need WithLocalOverrides")
	// ErrLocalOverrideRefused is returned by LocalOverrides.Set when the ruleset's
	// environment is protected.
	ErrLocalOverrideRefused = errors.New("flagnetic: local override refused in a protected environment")
)

// LocalOverrides manages the local overrides of a client: substitutions of flag
// values inside this process that never reach the server. An overridden
// flag reads reason local_override, with no experiment block and the stage
// unchanged. Obtain it with Client.LocalOverrides.
type LocalOverrides struct {
	c *Client
}

// localOverride is a substituted value normalized to its flag type: bool,
// string, float64 or json.RawMessage.
type localOverride struct {
	typ   string
	value any
}

// served returns the value a getter hands out; JSON is copied so the caller
// cannot modify the held override.
func (o localOverride) served() any {
	if raw, ok := o.value.(json.RawMessage); ok {
		return json.RawMessage(bytes.Clone(raw))
	}
	return o.value
}

// LocalOverrides returns the local overrides of the client.
func (c *Client) LocalOverrides() LocalOverrides {
	return LocalOverrides{c}
}

// Set substitutes value for the flag key. The value must be of the flag's
// type: a bool, a string, any Go number, or for a JSON flag anything
// encoding/json can marshal. A key the ruleset lacks takes the type of the
// value. Set fails with ErrLocalOverridesNotAllowed without WithLocalOverrides,
// and with ErrLocalOverrideRefused when the ruleset's environment is
// protected; both cases log override_refused (conformance/protocol
// scenarios.json). On success it logs override_set and fires the OnChange
// listeners.
func (o LocalOverrides) Set(key string, value any) error {
	c := o.c
	if c == nil || !c.allowOverrides {
		if c != nil {
			c.log(slog.LevelWarn, EventOverrideRefused, map[string]any{"flag": key})
		}
		return ErrLocalOverridesNotAllowed
	}
	loaded := c.ruleset.Load()
	if loaded != nil && loaded.rs.Protected() {
		c.log(slog.LevelWarn, EventOverrideRefused, map[string]any{
			"flag":        key,
			"environment": loaded.rs.Environment.Key,
		})
		return fmt.Errorf("local override of %q: %w", key, ErrLocalOverrideRefused)
	}
	typ := ""
	if loaded != nil {
		if f, ok := loaded.rs.Flag(key); ok {
			typ = f.Type
		}
	}
	lo, err := normalize(typ, value)
	if err != nil {
		return fmt.Errorf("flagnetic: local override of %q: %w", key, err)
	}
	c.overridesMu.Lock()
	next := maps.Clone(c.currentOverrides())
	if next == nil {
		next = map[string]localOverride{}
	}
	next[key] = lo
	c.overrides.Store(&next)
	c.overridesMu.Unlock()

	c.log(slog.LevelWarn, EventOverrideSet, map[string]any{"flag": key})
	c.fireChange([]string{key})
	return nil
}

// Remove drops the local override of key, firing the OnChange listeners
// when there was one.
func (o LocalOverrides) Remove(key string) {
	c := o.c
	if c == nil {
		return
	}
	c.overridesMu.Lock()
	current := c.currentOverrides()
	if _, ok := current[key]; !ok {
		c.overridesMu.Unlock()
		return
	}
	next := maps.Clone(current)
	delete(next, key)
	c.overrides.Store(&next)
	c.overridesMu.Unlock()
	c.fireChange([]string{key})
}

// Clear drops every local override, firing the OnChange listeners with the
// keys that had one.
func (o LocalOverrides) Clear() {
	c := o.c
	if c == nil {
		return
	}
	c.overridesMu.Lock()
	current := c.currentOverrides()
	c.overrides.Store(nil)
	c.overridesMu.Unlock()
	keys := make([]string, 0, len(current))
	for k := range current {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	c.fireChange(keys)
}

// List returns the local overrides by flag key: bool, string, float64 or
// json.RawMessage values.
func (o LocalOverrides) List() map[string]any {
	out := map[string]any{}
	if o.c == nil {
		return out
	}
	for k, lo := range o.c.currentOverrides() {
		out[k] = lo.served()
	}
	return out
}

func (c *Client) currentOverrides() map[string]localOverride {
	if p := c.overrides.Load(); p != nil {
		return *p
	}
	return nil
}

// refuseLocalOverrides drops every local override when rs belongs to a
// protected environment, logging override_refused for each, and returns
// the dropped keys sorted.
func (c *Client) refuseLocalOverrides(rs *eval.Ruleset) []string {
	if !rs.Protected() {
		return nil
	}
	c.overridesMu.Lock()
	current := c.currentOverrides()
	c.overrides.Store(nil)
	c.overridesMu.Unlock()
	keys := make([]string, 0, len(current))
	for k := range current {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c.log(slog.LevelWarn, EventOverrideRefused, map[string]any{"flag": k, "environment": rs.Environment.Key})
	}
	return keys
}

// localOverride returns the local override of key, if any may apply: a
// protected ruleset never serves one, even in the moment between its swap
// and refuseLocalOverrides.
func (c *Client) localOverride(loaded *loadedRuleset, key string) (localOverride, bool) {
	p := c.overrides.Load()
	if p == nil {
		return localOverride{}, false
	}
	if loaded != nil && loaded.rs.Protected() {
		return localOverride{}, false
	}
	lo, ok := (*p)[key]
	return lo, ok
}

// normalize converts value to typ, or infers typ from value when empty.
func normalize(typ string, value any) (localOverride, error) {
	if typ == "" {
		typ = requestedType(value)
	}
	switch typ {
	case typeBoolean:
		if v, ok := value.(bool); ok {
			return localOverride{typ, v}, nil
		}
	case typeString:
		if v, ok := value.(string); ok {
			return localOverride{typ, v}, nil
		}
	case typeNumber:
		if v, ok := toFloat(value); ok {
			return localOverride{typ, v}, nil
		}
	case typeJSON:
		if raw, ok := value.(json.RawMessage); ok {
			if !json.Valid(raw) {
				return localOverride{}, errors.New("invalid JSON")
			}
			return localOverride{typ, json.RawMessage(bytes.Clone(raw))}, nil
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return localOverride{}, err
		}
		return localOverride{typ, json.RawMessage(raw)}, nil
	default:
		return localOverride{}, fmt.Errorf("flag type %q is unknown", typ)
	}
	return localOverride{}, fmt.Errorf("a %T is not a value of a %s flag", value, typ)
}

func toFloat(v any) (float64, bool) {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	default:
		return 0, false
	}
}
