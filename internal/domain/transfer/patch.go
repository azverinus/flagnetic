package transfer

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
)

// entryValue returns v as the generic JSON value a document writes it as, so
// that two entries are compared, and patched, as the document shows them:
// what the encoding leaves out or writes as an empty value does not differ.
func entryValue(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		panic("transfer: a document entry is not JSON: " + err.Error())
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		panic("transfer: a document entry is not read back: " + err.Error())
	}
	return out
}

// patchOf returns the RFC 6902 patch turning before into after, both generic
// JSON values: nil when they are the same. Objects are compared member by
// member in byte order and lists element by element; a list that is longer
// gains its tail, one that is shorter loses it from the end, so that every
// operation applies to the document the ones before it left.
func patchOf(before, after any) []PatchOperation {
	var out []PatchOperation
	diffValue("", before, after, &out)
	return out
}

func diffValue(path string, before, after any, out *[]PatchOperation) {
	switch b := before.(type) {
	case map[string]any:
		if a, ok := after.(map[string]any); ok {
			diffObject(path, b, a, out)
			return
		}
	case []any:
		if a, ok := after.([]any); ok {
			diffList(path, b, a, out)
			return
		}
	}
	if !reflect.DeepEqual(before, after) {
		*out = append(*out, PatchOperation{Op: "replace", Path: path, Value: after})
	}
}

func diffObject(path string, before, after map[string]any, out *[]PatchOperation) {
	keys := make([]string, 0, len(before)+len(after))
	for k := range before {
		keys = append(keys, k)
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		at := path + "/" + escapePointer(k)
		b, inBefore := before[k]
		a, inAfter := after[k]
		switch {
		case !inAfter:
			*out = append(*out, PatchOperation{Op: "remove", Path: at})
		case !inBefore:
			*out = append(*out, PatchOperation{Op: "add", Path: at, Value: a})
		default:
			diffValue(at, b, a, out)
		}
	}
}

func diffList(path string, before, after []any, out *[]PatchOperation) {
	shared := min(len(before), len(after))
	for i := range shared {
		diffValue(path+"/"+strconv.Itoa(i), before[i], after[i], out)
	}
	for i := len(before) - 1; i >= shared; i-- {
		*out = append(*out, PatchOperation{Op: "remove", Path: path + "/" + strconv.Itoa(i)})
	}
	for i := shared; i < len(after); i++ {
		*out = append(*out, PatchOperation{Op: "add", Path: path + "/" + strconv.Itoa(i), Value: after[i]})
	}
}
