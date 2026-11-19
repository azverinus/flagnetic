package operations

import (
	"slices"
	"testing"
)

const bodiesDoc = `
paths:
  /things:
    put:
      operationId: putThing
      x-permission: member
      requestBody:
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/Thing"
  /import:
    post:
      operationId: importThing
      x-permission: member
      requestBody:
        content:
          application/json:
            schema: {$ref: "#/components/schemas/Thing"}
          application/yaml:
            schema: {type: string}
components:
  schemas:
    Thing:
      allOf:
        - $ref: "#/components/schemas/Base"
        - type: object
          required: [rules]
          properties:
            rules:
              type: array
              items: {$ref: "#/components/schemas/Rule"}
            note: {type: string, nullable: true}
    Base:
      type: object
      required: [name, note]
      properties:
        name: {type: string}
    Rule:
      type: object
      required: [on]
      properties:
        on: {type: boolean}
        next: {$ref: "#/components/schemas/Rule"}
`

func TestTheShapeOfABodyMergesAllOfAndFollowsReferencesAndCycles(t *testing.T) {
	ops, shapes, _, err := ReadAll([]byte(bodiesDoc))
	if err != nil {
		t.Fatal(err)
	}
	thing := shapes[ops["PUT /things"].Body]
	if !thing.Object || !slices.Equal(thing.Required, []string{"name", "note", "rules"}) || !thing.Deep {
		t.Errorf("thing = %+v, want an object requiring name, note and rules", thing)
	}
	byName := map[string]Member{}
	for _, m := range thing.Members {
		byName[m.Name] = m
	}
	if !byName["note"].Nullable || byName["name"].Nullable {
		t.Errorf("members = %+v, want note nullable and name not", thing.Members)
	}
	ruleID := shapes[byName["rules"].Shape].Items
	rule := shapes[ruleID]
	if !slices.Equal(rule.Required, []string{"on"}) || len(rule.Members) != 2 || rule.Members[0].Name != "next" || rule.Members[0].Shape != ruleID {
		t.Errorf("rule = %+v, want on required and next the rule's own shape", rule)
	}
	if got := ops["POST /import"].Body; got != 0 {
		t.Errorf("a body that may also be YAML has shape %d, want none to check", got)
	}
}

const oneOfDoc = `
paths:
  /proposals:
    post:
      operationId: propose
      x-permission: member
      requestBody:
        content:
          application/json:
            schema:
              type: object
              required: [proposed]
              properties:
                proposed:
                  oneOf:
                    - $ref: "#/components/schemas/Switch"
                    - $ref: "#/components/schemas/Note"
components:
  schemas:
    Switch:
      type: object
      required: ["on"]
      properties:
        "on": {type: boolean}
    Note:
      type: object
      properties:
        text: {type: string}
`

// Which alternative of a oneOf a body is cannot be told from the schema, so
// each alternative gets its own shape, by name, for the handler that knows.
func TestEachAlternativeOfAOneOfHasItsShapeByName(t *testing.T) {
	_, shapes, named, err := ReadAll([]byte(oneOfDoc))
	if err != nil {
		t.Fatal(err)
	}
	sw, ok := named["Switch"]
	if !ok || !slices.Equal(shapes[sw].Required, []string{"on"}) || !shapes[sw].Deep {
		t.Errorf("Switch = %d %+v, want a shape requiring on", sw, shapes[sw])
	}
	if note, ok := named["Note"]; !ok || shapes[note].Deep || !shapes[note].Object {
		t.Errorf("Note = %d %+v, want an object with nothing to check", note, shapes[note])
	}
}
