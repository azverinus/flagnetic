// Package text is the one rule for the text a client sends: it holds no
// control character other than a tab or a line break. PostgreSQL stores no
// U+0000 in text or jsonb and refuses the whole statement, so a request
// that carried one would be a 500 raised at will, and the other control
// characters mean nothing in a name, a note or a value and only trouble the
// terminals and logs that print them.
package text

import (
	"bytes"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// Code is the stable name of the rule, as the violation's code and as the
// error's.
const Code = "invalid_character"

// MaxViolations bounds the violations one request names, so the answer
// stays small however much text is refused.
const MaxViolations = 20

// Allowed reports whether s holds no control character other than a tab, a
// line feed or a carriage return.
func Allowed(s string) bool {
	return strings.IndexFunc(s, refused) < 0
}

// AllowedURL reports whether the path of u and every name and value of its
// query, decoded, hold no refused character.
func AllowedURL(u *url.URL) bool {
	if !Allowed(u.Path) {
		return false
	}
	for name, values := range u.Query() {
		if !Allowed(name) {
			return false
		}
		for _, v := range values {
			if !Allowed(v) {
				return false
			}
		}
	}
	return true
}

func refused(r rune) bool {
	return unicode.IsControl(r) && r != '\t' && r != '\n' && r != '\r'
}

var pointerToken = strings.NewReplacer("~", "~0", "/", "~1")

func violation(pointer string) apperrors.Violation {
	return apperrors.Violation{
		Pointer: pointer, Code: Code,
		Detail: "text must not contain control characters other than a tab or a line break",
	}
}

// Error returns the invalid error that names violations, or nil for none.
func Error(violations []apperrors.Violation) error {
	if len(violations) == 0 {
		return nil
	}
	return apperrors.Invalid(Code, "the request holds text with a control character", violations...)
}

// collector gathers violations up to MaxViolations.
type collector struct{ found []apperrors.Violation }

func (c *collector) add(pointer string) {
	if len(c.found) < MaxViolations {
		c.found = append(c.found, violation(pointer))
	}
}

// InJSON returns a violation for every string and member name of the JSON
// document body that holds a refused character, by JSON Pointer, in the
// order they appear. A body that is no JSON gives what was found before the
// fault: its own decoder refuses it, with its own words.
func InJSON(body []byte) []apperrors.Violation {
	if bytes.IndexByte(body, '\\') < 0 && bytes.IndexFunc(body, refused) < 0 {
		// No escape and no control character written out: the common body
		// is not decoded twice.
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	c := &collector{}
	walkJSON(dec, "", c)
	return c.found
}

func walkJSON(dec *json.Decoder, pointer string, c *collector) bool {
	tok, err := dec.Token()
	if err != nil {
		return false
	}
	switch v := tok.(type) {
	case string:
		if !Allowed(v) {
			c.add(pointer)
		}
	case json.Delim:
		switch v {
		case '{':
			for dec.More() {
				name, err := dec.Token()
				if err != nil {
					return false
				}
				key, _ := name.(string)
				at := pointer + "/" + pointerToken.Replace(key)
				if !Allowed(key) {
					c.add(at)
				}
				if !walkJSON(dec, at, c) {
					return false
				}
			}
		case '[':
			for i := 0; dec.More(); i++ {
				if !walkJSON(dec, pointer+"/"+strconv.Itoa(i), c) {
					return false
				}
			}
		}
		if _, err := dec.Token(); err != nil {
			return false
		}
	}
	return true
}

// InTree returns a violation for every string and map key of a decoded
// document (maps of string to any, lists, strings) that holds a refused
// character, by JSON Pointer, in the order of the document, map members
// sorted by name.
func InTree(tree any) []apperrors.Violation {
	c := &collector{}
	walkTree(tree, "", c)
	return c.found
}

func walkTree(v any, pointer string, c *collector) {
	switch v := v.(type) {
	case string:
		if !Allowed(v) {
			c.add(pointer)
		}
	case []any:
		for i, item := range v {
			walkTree(item, pointer+"/"+strconv.Itoa(i), c)
		}
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			at := pointer + "/" + pointerToken.Replace(k)
			if !Allowed(k) {
				c.add(at)
			}
			walkTree(v[k], at, c)
		}
	}
}
