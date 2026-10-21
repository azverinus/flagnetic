// Package flagnetictest builds offline Flagnetic clients for deterministic
// tests: from a ruleset document, a file, or a map of flag key to value.
// No client it builds ever touches the network.
//
//	c := flagnetictest.FromValues(t, map[string]any{"new_checkout": true})
//	handler := NewHandler(c.Client)
//	c.Set("new_checkout", false) // the flag now serves false as fallthrough
//
// Exposures and aliases go to the client's Recorder; AssertExposure reads
// the one an experiment flag left.
package flagnetictest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// variationKey names the one variation Set and FromValues give a flag.
const variationKey = "value"

// Client is an offline flagnetic.Client whose document a test can rewrite.
type Client struct {
	*flagnetic.Client

	t    testing.TB
	read func() ([]byte, error)

	mu       sync.Mutex
	document []byte
}

// New builds an offline client from a ruleset document, exactly as the
// ruleset endpoint serves it. It fails the test when the document does not
// decode.
func New(t testing.TB, document []byte, opts ...flagnetic.Option) *Client {
	t.Helper()
	doc := bytes.Clone(document)
	return build(t, func() ([]byte, error) { return doc, nil }, opts)
}

// FromFile builds an offline client from the ruleset document at path.
// Reload re-reads the file.
func FromFile(t testing.TB, path string, opts ...flagnetic.Option) *Client {
	t.Helper()
	return build(t, func() ([]byte, error) { return os.ReadFile(path) }, opts) // #nosec G304 -- the test names its own document
}

// FromValues builds an offline client serving each value as the
// fallthrough of a flag with one variation. The value's Go type decides the
// flag type: bool is boolean, string is string, any Go number is number,
// anything else is JSON.
func FromValues(t testing.TB, values map[string]any, opts ...flagnetic.Option) *Client {
	t.Helper()
	doc := map[string]any{"schema_version": 1, "version": 1, "scope": "server", "flags": []any{}}
	for key, value := range values {
		if err := setFlag(doc, key, value); err != nil {
			t.Fatalf("flagnetictest: %v", err)
		}
	}
	document, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("flagnetictest: %v", err)
	}
	return build(t, func() ([]byte, error) { return document, nil }, opts)
}

func build(t testing.TB, read func() ([]byte, error), opts []flagnetic.Option) *Client {
	t.Helper()
	document, err := read()
	if err != nil {
		t.Fatalf("flagnetictest: read document: %v", err)
	}
	tc := &Client{t: t, read: read, document: document}
	source := func() ([]byte, error) {
		tc.mu.Lock()
		defer tc.mu.Unlock()
		return tc.document, nil
	}
	c, err := flagnetic.New("", "", append([]flagnetic.Option{flagnetic.WithOfflineSource(source)}, opts...)...)
	if err != nil {
		t.Fatalf("flagnetictest: %v", err)
	}
	// The client reports its evaluation counts on an interval, so it holds
	// a goroutine until it is closed.
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	tc.Client = c
	return tc
}

// Set rewrites the flag key to serve value to everyone: one variation,
// served as fallthrough, so the reason reads fallthrough. A key the
// document lacks is added, typed by the value like FromValues; an existing
// flag keeps its type and its value must be of that type. OnChange
// listeners fire.
func (c *Client) Set(key string, value any) {
	c.t.Helper()
	c.mu.Lock()
	var doc map[string]any
	err := json.Unmarshal(c.document, &doc)
	if err == nil {
		err = setFlag(doc, key, value)
	}
	if err == nil {
		c.document, err = json.Marshal(doc)
	}
	c.mu.Unlock()
	if err != nil {
		c.t.Fatalf("flagnetictest: set %q: %v", key, err)
	}
	if err := c.Client.Reload(); err != nil {
		c.t.Fatalf("flagnetictest: set %q: %v", key, err)
	}
}

// Reload re-reads the source the client was built from, discarding what
// Set changed. OnChange listeners fire with the flags that changed.
func (c *Client) Reload() {
	c.t.Helper()
	document, err := c.read()
	if err != nil {
		c.t.Fatalf("flagnetictest: reload: %v", err)
	}
	c.mu.Lock()
	c.document = document
	c.mu.Unlock()
	if err := c.Client.Reload(); err != nil {
		c.t.Fatalf("flagnetictest: reload: %v", err)
	}
}

// AssertExposure fails the test unless exactly one exposure was recorded
// for the flag key, and returns it. Repeats per experiment, unit and
// variation are suppressed before they reach the recorder, so a handler
// that evaluates and reports the same assignment twice still leaves one.
//
//	e := c.AssertExposure("pricing")
//	if e.VariationKey != "treatment" { ... }
func (c *Client) AssertExposure(key string) flagnetic.Exposure {
	c.t.Helper()
	var found []flagnetic.Exposure
	for _, e := range c.Recorder().Exposures() {
		if e.FlagKey == key {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		c.t.Fatalf("flagnetictest: %d exposures recorded for %q, want exactly 1", len(found), key)
		return flagnetic.Exposure{}
	}
	return found[0]
}

// setFlag replaces or adds the flag key in a decoded ruleset document.
func setFlag(doc map[string]any, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("value of %q: %w", key, err)
	}
	flags, _ := doc["flags"].([]any)
	typ := flagType(value)
	at := -1
	for i, f := range flags {
		if m, ok := f.(map[string]any); ok && m["key"] == key {
			at = i
			if t, ok := m["type"].(string); ok {
				if t != "json" && typ != t {
					return fmt.Errorf("flag %q is %s, a %T is %s", key, t, value, typ)
				}
				typ = t
			}
		}
	}
	flag := map[string]any{
		"key":                key,
		"type":               typ,
		"automatic_exposure": false,
		"on":                 true,
		"variations":         []any{map[string]any{"key": variationKey, "value": json.RawMessage(raw)}},
		"off_variation":      variationKey,
		"fallthrough":        variationKey,
	}
	if at >= 0 {
		flags[at] = flag
	} else {
		flags = append(flags, flag)
	}
	doc["flags"] = flags
	return nil
}

func flagType(value any) string {
	switch value.(type) {
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, uintptr:
		return "number"
	default:
		return "json"
	}
}
