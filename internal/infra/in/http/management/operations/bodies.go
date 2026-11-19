package operations

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Shape is what a JSON request body must hold, read from the schema of an
// operation's request: the members it requires, and where to look for more
// below them. Shapes are numbered; 0 is no shape, so a zero Entry.Body says
// the operation takes no JSON body to check. The numbering is stable for one
// document, so the table can name them.
//
// oapi-codegen decodes a body with encoding/json and never checks `required`,
// so a client that leaves out a required boolean or list would hand its zero
// value to the use case, and a missing `protected` would unprotect an
// environment. Checking the body against its shape, once, at the adapter
// covers every operation, those added later too.
type Shape struct {
	// Object says the value must be a JSON object.
	Object bool
	// Required names the members that must be present, and not null unless the
	// member says it may be (or its schema gives it no type, so null is as good
	// a value as any).
	Required []string
	// Members are the members to look into: those that may be null, those that
	// hold something to check.
	Members []Member
	// Items is the shape of the elements of an array, 0 for none to check.
	Items int
	// Values is the shape of the values of a map, 0 for none to check.
	Values int
	// Deep says a value of this shape holds something to check at some depth,
	// so a shape without it is not walked.
	Deep bool
}

// Member is a member of an object whose value is checked or may be null.
type Member struct {
	Name     string
	Shape    int
	Nullable bool
}

// schemaRef is the prefix of a reference to a schema of the document.
const schemaRef = "#/components/schemas/"

// shapes builds the shapes of the request bodies of a document.
type shapes struct {
	schemas map[string]map[string]any
	list    []Shape
	byKey   map[string]int
	err     []error
}

// readShapes returns the shapes of the document's JSON request bodies and the
// shape each operation's body has, keyed by route pattern.
func readShapes(doc []byte) ([]Shape, map[string]int, error) {
	var d struct {
		Paths      map[string]map[string]any `yaml:"paths"`
		Components struct {
			Schemas map[string]map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(doc, &d); err != nil {
		return nil, nil, fmt.Errorf("reading the document: %w", err)
	}
	s := &shapes{schemas: d.Components.Schemas, list: []Shape{{}}, byKey: map[string]int{}}
	bodies := map[string]int{}
	paths := sortedKeys(d.Paths)
	for _, path := range paths {
		for _, method := range methods {
			op, ok := d.Paths[path][method].(map[string]any)
			if !ok {
				continue
			}
			schema := jsonBody(op)
			if schema == nil {
				continue
			}
			where := strings.ToUpper(method) + " " + path
			if id := s.build("body "+where, schema); id != 0 {
				bodies[where] = id
			}
		}
	}
	s.markDeep()
	if len(s.err) > 0 {
		return nil, nil, fmt.Errorf("reading the request bodies: %w", joinErrors(s.err))
	}
	return s.list, bodies, nil
}

// jsonBody returns the schema of an operation's JSON request body, or nil when
// it has none or also takes another media type (a YAML document is no JSON
// body to check member by member).
func jsonBody(op map[string]any) map[string]any {
	body, _ := op["requestBody"].(map[string]any)
	content, _ := body["content"].(map[string]any)
	if len(content) != 1 {
		return nil
	}
	media, _ := content["application/json"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	return schema
}

// flat is a schema with its allOf parts merged.
type flat struct {
	object   bool
	nullable bool
	// untyped says no part of the schema gives the value a type, so any JSON
	// value, null among them, is one the schema holds.
	typed    bool
	required []string
	props    map[string]map[string]any
	items    map[string]any
	values   map[string]any
}

// resolve follows a reference to the schema it names, and says by what name.
func (s *shapes) resolve(schema map[string]any) (map[string]any, string) {
	ref, ok := schema["$ref"].(string)
	if !ok {
		return schema, ""
	}
	name, found := strings.CutPrefix(ref, schemaRef)
	target, exists := s.schemas[name]
	if !found || !exists {
		s.err = append(s.err, fmt.Errorf("a reference to %q the document does not define", ref))
		return map[string]any{}, ""
	}
	return target, name
}

// flatten merges schema and the schemas it is allOf. A oneOf or anyOf is not
// looked into: which alternative a body is cannot be told from its schema.
func (s *shapes) flatten(schema map[string]any, into *flat) {
	schema, _ = s.resolve(schema)
	if schema["type"] == "object" || schema["properties"] != nil || schema["required"] != nil || schema["additionalProperties"] != nil {
		into.object = true
	}
	if schema["type"] != nil || schema["enum"] != nil || into.object || schema["items"] != nil {
		into.typed = true
	}
	if n, _ := schema["nullable"].(bool); n {
		into.nullable = true
	}
	if parts, ok := schema["allOf"].([]any); ok {
		for _, part := range parts {
			if m, ok := part.(map[string]any); ok {
				s.flatten(m, into)
			}
		}
	}
	for _, r := range asList(schema["required"]) {
		if !slices.Contains(into.required, r) {
			into.required = append(into.required, r)
		}
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		for name, p := range props {
			if m, ok := p.(map[string]any); ok {
				if into.props == nil {
					into.props = map[string]map[string]any{}
				}
				if _, seen := into.props[name]; !seen {
					into.props[name] = m
				}
			}
		}
	}
	if items, ok := schema["items"].(map[string]any); ok && into.items == nil {
		into.items = items
	}
	if values, ok := schema["additionalProperties"].(map[string]any); ok && into.values == nil {
		into.values = values
	}
}

// build returns the number of the shape of schema, found at key, building it
// first if it is new; a schema referred to by name has the one shape wherever
// it is used. 0 says there is nothing to check.
func (s *shapes) build(key string, schema map[string]any) int {
	if _, name := s.resolve(schema); name != "" {
		key = schemaRef + name
	}
	if id, ok := s.byKey[key]; ok {
		return id
	}
	id := len(s.list)
	s.byKey[key] = id
	s.list = append(s.list, Shape{})
	var f flat
	s.flatten(schema, &f)
	shape := Shape{Object: f.object, Required: sortedCopy(f.required)}
	for _, name := range sortedKeys(f.props) {
		p := f.props[name]
		var pf flat
		s.flatten(p, &pf)
		child := s.build(key+"/properties/"+name, p)
		shape.Members = append(shape.Members, Member{Name: name, Shape: child, Nullable: pf.nullable || !pf.typed})
	}
	if f.items != nil {
		shape.Items = s.build(key+"/items", f.items)
	}
	if f.values != nil {
		shape.Values = s.build(key+"/additionalProperties", f.values)
	}
	s.list[id] = shape
	return id
}

// markDeep sets Deep on every shape that requires a member or holds, at any
// depth, a shape that does, and drops what leads nowhere, so a body is walked
// only where something can be missing. It is a fixpoint, since shapes may
// refer to themselves.
func (s *shapes) markDeep() {
	for i := range s.list {
		s.list[i].Deep = len(s.list[i].Required) > 0
	}
	deep := func(i int) bool { return i != 0 && s.list[i].Deep }
	for changed := true; changed; {
		changed = false
		for i, sh := range s.list {
			if sh.Deep {
				continue
			}
			found := deep(sh.Items) || deep(sh.Values)
			for _, m := range sh.Members {
				found = found || deep(m.Shape)
			}
			if found {
				s.list[i].Deep = true
				changed = true
			}
		}
	}
	for i, sh := range s.list {
		if !sh.Deep {
			s.list[i] = Shape{Object: sh.Object}
			continue
		}
		if !deep(sh.Items) {
			sh.Items = 0
		}
		if !deep(sh.Values) {
			sh.Values = 0
		}
		kept := sh.Members[:0:0]
		for _, m := range sh.Members {
			if !deep(m.Shape) {
				m.Shape = 0
			}
			if m.Shape != 0 || slices.Contains(sh.Required, m.Name) {
				kept = append(kept, m)
			}
		}
		sh.Members = kept
		s.list[i] = sh
	}
}

func asList(v any) []string {
	items, _ := v.([]any)
	out := make([]string, 0, len(items))
	for _, i := range items {
		if s, ok := i.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func sortedCopy(in []string) []string {
	out := slices.Clone(in)
	sort.Strings(out)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func joinErrors(errs []error) error {
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return fmt.Errorf("%s", strings.Join(slices.Compact(msgs), "; "))
}
