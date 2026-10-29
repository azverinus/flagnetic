package transfer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// MalformedError is input that is not a document in the format at all: text
// that does not parse, or that holds more than one document. The adapter
// answers it as a request it cannot decode, where a document that parses but
// breaks a rule is answered with pointers into it.
type MalformedError struct{ Detail string }

func (e *MalformedError) Error() string { return "malformed project document: " + e.Detail }

// Decode reads a document from data written in f. It is strict, so that a
// document from a newer server fails loudly on an older one instead of
// importing with silent loss: a field the document does not define is refused
// naming its JSON Pointer, as is a value of the wrong type, and so is data
// after the document. Every fault found is reported at once.
//
// A schema_version this server does not read is the invalid error
// schema_version_unsupported, naming the versions it does; any other fault
// is the invalid error invalid_document with a violation per fault.
func Decode(f Format, data []byte) (Document, error) {
	var tree any
	var err error
	switch f {
	case JSON:
		tree, err = parseJSON(data)
	case YAML:
		tree, err = parseYAML(data)
	default:
		return Document{}, fmt.Errorf("decoding the project document: unknown format %q", f)
	}
	if err != nil {
		return Document{}, err
	}
	root, ok := tree.(map[string]any)
	if !ok {
		return Document{}, &MalformedError{Detail: "a project document is an object"}
	}
	if err := checkVersion(root); err != nil {
		return Document{}, err
	}
	w := &walker{}
	w.check("", root, reflect.TypeFor[Document]())
	if len(w.violations) > 0 {
		return Document{}, apperrors.Invalid("invalid_document", "the project document is not valid", w.violations...)
	}
	// The tree is known to fit the types, so this cannot fail on its shape.
	b, err := json.Marshal(root)
	if err != nil {
		return Document{}, fmt.Errorf("decoding the project document: %w", err)
	}
	var d Document
	if err := json.Unmarshal(b, &d); err != nil {
		return Document{}, fmt.Errorf("decoding the project document: %w", err)
	}
	return d, nil
}

func checkVersion(root map[string]any) error {
	v, ok := root["schema_version"]
	if !ok {
		return apperrors.Invalid("invalid_document", "the project document is not valid", apperrors.Violation{
			Pointer: "/schema_version", Code: "required", Detail: "a document names the schema version it is written in",
			Params: map[string]any{"supported_versions": SupportedVersions},
		})
	}
	n, isNumber := v.(json.Number)
	version, err := n.Int64()
	if isNumber && err == nil && slices.Contains(SupportedVersions, int(version)) {
		return nil
	}
	return apperrors.Invalid("schema_version_unsupported", "this server does not read this schema_version of the project document").
		WithExtension("supported_versions", SupportedVersions)
}

// parseJSON reads the one JSON value data holds, numbers kept as written.
func parseJSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, &MalformedError{Detail: "the JSON does not parse: " + err.Error()}
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, &MalformedError{Detail: "the JSON holds more than one value"}
	}
	return v, nil
}

// parseYAML reads the one YAML document data holds into the tree JSON would
// give: maps by string key, numbers as json.Number and instants as text.
func parseYAML(data []byte) (any, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, &MalformedError{Detail: "the YAML does not parse: " + err.Error()}
	}
	var second any
	if err := dec.Decode(&second); !errors.Is(err, io.EOF) {
		return nil, &MalformedError{Detail: "the YAML holds more than one document"}
	}
	return jsonTree(v)
}

// jsonTree converts what the YAML decoder gives into what the JSON decoder
// would, so one walk checks both.
func jsonTree(v any) (any, error) {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, e := range v {
			c, err := jsonTree(e)
			if err != nil {
				return nil, err
			}
			out[k] = c
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, e := range v {
			c, err := jsonTree(e)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	case int:
		return json.Number(strconv.Itoa(v)), nil
	case int64:
		return json.Number(strconv.FormatInt(v, 10)), nil
	case uint64:
		return json.Number(strconv.FormatUint(v, 10)), nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, &MalformedError{Detail: "the YAML holds a number JSON cannot"}
		}
		return json.Number(strconv.FormatFloat(v, 'g', -1, 64)), nil
	case time.Time:
		return v.UTC().Format(time.RFC3339Nano), nil
	case string, bool, nil:
		return v, nil
	case map[any]any:
		return nil, &MalformedError{Detail: "the YAML holds a mapping whose key is not text"}
	}
	return nil, &MalformedError{Detail: fmt.Sprintf("the YAML holds a %T, which a document cannot", v)}
}

// walker checks a tree against the types of a document, collecting a
// violation for each fault with the JSON Pointer to it.
type walker struct{ violations []apperrors.Violation }

func (w *walker) add(pointer, code, detail string) {
	w.violations = append(w.violations, apperrors.Violation{Pointer: pointer, Code: code, Detail: detail})
}

var (
	timeType    = reflect.TypeFor[time.Time]()
	dateType    = reflect.TypeFor[Date]()
	rawType     = reflect.TypeFor[json.RawMessage]()
	numberKinds = map[reflect.Kind]bool{
		reflect.Int: true, reflect.Int8: true, reflect.Int16: true, reflect.Int32: true, reflect.Int64: true,
	}
)

// check walks v, found at pointer, against t. A value of a type that holds
// anything (a condition's value, a variation's value) is not looked into.
func (w *walker) check(pointer string, v any, t reflect.Type) {
	nullable := false
	for t.Kind() == reflect.Pointer {
		t, nullable = t.Elem(), true
	}
	if t == rawType || t.Kind() == reflect.Interface {
		return
	}
	if v == nil {
		if !nullable && t.Kind() != reflect.Slice && t.Kind() != reflect.Map {
			w.add(pointer, "invalid_type", "must not be null")
		}
		return
	}
	switch {
	case t == timeType:
		if s, ok := v.(string); !ok {
			w.add(pointer, "invalid_type", "must be a string")
		} else if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
			w.add(pointer, "invalid_value", "must be an RFC 3339 instant, such as 2026-01-01T00:00:00Z")
		}
	case t == dateType:
		if s, ok := v.(string); !ok {
			w.add(pointer, "invalid_type", "must be a string")
		} else if err := new(Date).UnmarshalJSON([]byte(strconv.Quote(s))); err != nil {
			w.add(pointer, "invalid_value", "must be a day, such as 2026-01-01")
		}
	case t.Kind() == reflect.Struct:
		w.object(pointer, v, t)
	case t.Kind() == reflect.Slice:
		list, ok := v.([]any)
		if !ok {
			w.add(pointer, "invalid_type", "must be a list")
			return
		}
		for i, e := range list {
			w.check(pointer+"/"+strconv.Itoa(i), e, t.Elem())
		}
	case t.Kind() == reflect.Map:
		m, ok := v.(map[string]any)
		if !ok {
			w.add(pointer, "invalid_type", "must be an object")
			return
		}
		for _, k := range sortedKeys(m) {
			w.check(pointer+"/"+escapePointer(k), m[k], t.Elem())
		}
	case t.Kind() == reflect.String:
		if _, ok := v.(string); !ok {
			w.add(pointer, "invalid_type", "must be a string")
		}
	case t.Kind() == reflect.Bool:
		if _, ok := v.(bool); !ok {
			w.add(pointer, "invalid_type", "must be true or false")
		}
	case numberKinds[t.Kind()]:
		if n, ok := v.(json.Number); !ok {
			w.add(pointer, "invalid_type", "must be an integer")
		} else if _, err := n.Int64(); err != nil {
			w.add(pointer, "invalid_type", "must be an integer")
		}
	}
}

// object checks the members of the object v against the fields of the struct
// t, in key order so that the violations of one document are always listed
// in one.
func (w *walker) object(pointer string, v any, t reflect.Type) {
	m, ok := v.(map[string]any)
	if !ok {
		w.add(pointer, "invalid_type", "must be an object")
		return
	}
	fields := map[string]reflect.Type{}
	for f := range t.Fields() {
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name != "" && name != "-" {
			fields[name] = f.Type
		}
	}
	for _, k := range sortedKeys(m) {
		ft, known := fields[k]
		if !known {
			w.add(pointer+"/"+escapePointer(k), "unknown_field", "the project document has no such field")
			continue
		}
		w.check(pointer+"/"+escapePointer(k), m[k], ft)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// escapePointer escapes a key as a JSON Pointer token (RFC 6901).
func escapePointer(key string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(key)
}
