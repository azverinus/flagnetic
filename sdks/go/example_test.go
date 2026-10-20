package flagnetic_test

import (
	"context"
	"encoding/json"
	"fmt"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

func Example() {
	c, err := flagnetic.New("", "", flagnetic.WithOffline([]byte(`{
	  "schema_version": 1, "version": 12, "scope": "server",
	  "unit_types": ["user"],
	  "attributes": [{"key": "country", "type": "string"}],
	  "flags": [
	    {"key": "new_checkout", "type": "boolean", "on": true,
	     "variations": [{"key": "on", "value": true}, {"key": "off", "value": false}],
	     "off_variation": "off", "fallthrough": "off",
	     "rules": [{"id": "de", "conditions": [{"kind": "attribute", "attribute": "country", "op": "eq", "value": "DE"}],
	                "serve": {"kind": "variation", "variation": "on"}}]},
	    {"key": "greeting", "type": "string", "on": false,
	     "variations": [{"key": "hello", "value": "Hello"}], "off_variation": "hello", "fallthrough": "hello"},
	    {"key": "page_size", "type": "number", "on": true,
	     "variations": [{"key": "large", "value": 50}], "off_variation": "large", "fallthrough": "large"},
	    {"key": "theme", "type": "json", "on": true,
	     "variations": [{"key": "dark", "value": {"background": "black"}}], "off_variation": "dark", "fallthrough": "dark"}
	  ]}`)), flagnetic.WithLocalOverrides())
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	defer func() { _ = c.Close(ctx) }()
	fc := flagnetic.Context{
		Units:      map[string]string{"user": "u-1001"},
		Attributes: map[string]any{"country": "DE"},
	}

	fmt.Println(c.Bool(ctx, fc, "new_checkout", false), c.Detail(ctx, fc, "new_checkout", false).Reason)
	fmt.Println(c.String(ctx, fc, "greeting", "Hi"), c.Detail(ctx, fc, "greeting", "Hi").Reason)
	fmt.Println(c.Int(ctx, fc, "page_size", 20), c.Detail(ctx, fc, "page_size", 20.0).Reason)
	fmt.Println(string(c.JSON(ctx, fc, "theme", nil)), c.Detail(ctx, fc, "theme", json.RawMessage(nil)).Reason)
	fmt.Println(c.Bool(ctx, fc, "missing", true), c.Detail(ctx, fc, "missing", true).Reason)

	_ = c.LocalOverrides().Set("new_checkout", false)
	fmt.Println(c.Bool(ctx, fc, "new_checkout", true), c.Detail(ctx, fc, "new_checkout", true).Reason)
	// Output:
	// true rule
	// Hello off
	// 50 fallthrough
	// {"background":"black"} fallthrough
	// true fallback
	// false local_override
}
