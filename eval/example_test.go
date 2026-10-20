package eval_test

import (
	"fmt"

	"github.com/azverinus/flagnetic/eval"
)

func ExampleRuleset_Evaluate() {
	rs, err := eval.Decode([]byte(`{
	  "schema_version": 1, "version": 42, "scope": "server",
	  "unit_types": ["user"],
	  "attributes": [{"key": "plan", "type": "string"}, {"key": "app_version", "type": "semver"}],
	  "flags": [{"key": "new_checkout", "type": "boolean", "on": true,
	    "variations": [{"key": "true", "value": true}, {"key": "false", "value": false}],
	    "off_variation": "false", "fallthrough": "false",
	    "rules": [{"id": "01J9", "conditions": [
	        {"kind": "attribute", "attribute": "plan", "op": "eq", "value": "pro"},
	        {"kind": "attribute", "attribute": "app_version", "op": "lt", "value": "1.17.0"}],
	      "serve": {"kind": "variation", "variation": "true"}}]}]
	}`))
	if err != nil {
		panic(err)
	}
	r := rs.Evaluate(eval.Context{
		Units:      map[string]string{"user": "u-1001"},
		Attributes: map[string]any{"plan": "pro", "app_version": "1.9.3"},
	}, "new_checkout")
	fmt.Println(string(r.Value), r.Reason, r.RuleID)
	// Output: true rule 01J9
}

func ExampleBucket() {
	// The same as ClickHouse:
	// SELECT murmurHash3_32('a3f9c2e17b4d0e58.u_10482') % 10000
	fmt.Println(eval.Bucket("a3f9c2e17b4d0e58", "u_10482"))
	// Output: 6884
}
