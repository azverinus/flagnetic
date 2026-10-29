package transfer

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Format is how a document is written: JSON is the canonical encoding and
// YAML is derived from it field for field.
type Format string

// The formats.
const (
	JSON Format = "json"
	YAML Format = "yaml"
)

// ParseFormat returns the format named name, and whether there is one.
func ParseFormat(name string) (Format, bool) {
	f := Format(name)
	return f, f == JSON || f == YAML
}

// Extension is the file extension of a document in f, without the dot.
func (f Format) Extension() string { return string(f) }

// Encode returns d written in f. The bytes depend on the document alone: two
// documents of one project are the same file wherever their lists were built
// in another order, so a diff of two exports shows real changes. The
// environments keep their order, which is their sort order, and so do the
// lists whose order serves (variations, rules, tags, an experiment's
// weights); every other list is by key, in byte order, and a map is too.
func Encode(f Format, d Document) ([]byte, error) {
	c, err := d.canonical()
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return nil, fmt.Errorf("encoding the project document: %w", err)
	}
	switch f {
	case JSON:
		return out.Bytes(), nil
	case YAML:
		return yamlFromJSON(out.Bytes())
	}
	return nil, fmt.Errorf("encoding the project document: unknown format %q", f)
}

// canonical returns a copy of d whose lists are in the order a document is
// written in and none of which is nil, so that an empty list is written as
// one.
func (d Document) canonical() (Document, error) {
	c := d
	c.Environments = nonNil(d.Environments)
	c.UnitTypes = sortedByKey(d.UnitTypes, func(u UnitType) string { return u.Key })
	c.Attributes = sortedByKey(d.Attributes, func(a Attribute) string { return a.Key })
	c.Segments = sortedByKey(d.Segments, func(s Segment) string { return s.Key })
	for i := range c.Segments {
		c.Segments[i].Conditions = nonNil(c.Segments[i].Conditions)
	}
	c.Flags = sortedByKey(d.Flags, func(f Flag) string { return f.Key })
	for i := range c.Flags {
		f, err := c.Flags[i].canonical()
		if err != nil {
			return Document{}, fmt.Errorf("encoding flag %q: %w", f.Key, err)
		}
		c.Flags[i] = f
	}
	c.Experiments = sortedByKey(d.Experiments, func(e Experiment) string { return e.Key })
	for i := range c.Experiments {
		e := &c.Experiments[i]
		e.Tags, e.Variations = nonNil(e.Tags), nonNil(e.Variations)
	}
	return c, nil
}

func (f Flag) canonical() (Flag, error) {
	f.Tags = nonNil(f.Tags)
	f.Variations = slices.Clone(nonNil(f.Variations))
	for i, v := range f.Variations {
		value, err := canonicalJSON(v.Value)
		if err != nil {
			return f, fmt.Errorf("variation %q: %w", v.Key, err)
		}
		f.Variations[i].Value = value
	}
	configurations := make(map[string]Configuration, len(f.Configurations))
	for env, c := range f.Configurations {
		c.Prerequisites = slices.Clone(nonNil(c.Prerequisites))
		slices.SortFunc(c.Prerequisites, func(a, b Prerequisite) int {
			return cmp.Or(cmp.Compare(a.Flag, b.Flag), cmp.Compare(a.Variation, b.Variation))
		})
		c.Overrides = slices.Clone(nonNil(c.Overrides))
		slices.SortFunc(c.Overrides, func(a, b Override) int {
			return cmp.Or(cmp.Compare(a.UnitType, b.UnitType), cmp.Compare(a.UnitID, b.UnitID))
		})
		c.Rules = slices.Clone(nonNil(c.Rules))
		for i := range c.Rules {
			c.Rules[i].Conditions = nonNil(c.Rules[i].Conditions)
		}
		configurations[env] = c
	}
	f.Configurations = configurations
	return f, nil
}

// sortedByKey returns the elements of in by key in byte order, never nil.
func sortedByKey[T any](in []T, key func(T) string) []T {
	out := slices.Clone(nonNil(in))
	slices.SortStableFunc(out, func(a, b T) int { return cmp.Compare(key(a), key(b)) })
	return out
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// yamlFromJSON writes the JSON document in as YAML, mapping for mapping and
// in the same order. A string a YAML 1.1 reader would take for something
// else (on, no, a date) is quoted by the encoder, so the document means the
// same to every reader.
func yamlFromJSON(in []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(in))
	dec.UseNumber()
	node, err := yamlNode(dec)
	if err != nil {
		return nil, fmt.Errorf("encoding the project document: %w", err)
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return nil, fmt.Errorf("encoding the project document: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encoding the project document: %w", err)
	}
	return out.Bytes(), nil
}

// yamlNode reads the next JSON value from dec as a YAML node.
func yamlNode(dec *json.Decoder) (*yaml.Node, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch v := tok.(type) {
	case json.Delim:
		if v == '{' {
			n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					return nil, err
				}
				val, err := yamlNode(dec)
				if err != nil {
					return nil, err
				}
				n.Content = append(n.Content, stringNode(k.(string)), val)
			}
			_, err := dec.Token()
			return n, err
		}
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for dec.More() {
			el, err := yamlNode(dec)
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, el)
		}
		_, err := dec.Token()
		return n, err
	case string:
		return stringNode(v), nil
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(v)}, nil
	case json.Number:
		tag := "!!int"
		if bytes.ContainsAny([]byte(v), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: v.String()}, nil
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}
	return nil, io.ErrUnexpectedEOF
}

// stringNode is a YAML string. The encoder quotes a string that YAML 1.2
// would read as another type, but not one that only YAML 1.1 reads as a
// boolean (on, off, yes, no, y, n), and a key such as on is one; a document
// is read by tools of both, so these are quoted here.
func stringNode(s string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	if slices.Contains(yaml11Booleans, strings.ToLower(s)) {
		n.Style = yaml.DoubleQuotedStyle
	}
	return n
}

var yaml11Booleans = []string{"y", "n", "yes", "no", "on", "off"}
