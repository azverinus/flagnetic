package management

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/azverinus/flagnetic/internal/infra/in/http/management/operations"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/text"
)

// bodyInvalid is the code of the 422 that answers a body missing what its
// operation requires.
const bodyInvalid = "invalid"

// pointerToken escapes a member name in a JSON Pointer (RFC 6901).
var pointerToken = strings.NewReplacer("~", "~0", "/", "~1")

// refuseMissingMembers refuses a JSON body that is no object, where the
// operation takes one, or that leaves out a member its schema requires (or
// sets one to null that may not be): 422 with a violation per member, by its
// pointer, as the domain answers its own. oapi-codegen decodes a body with
// encoding/json and checks no `required`, so a client that left out a boolean
// or a list would hand its zero value to the use case: `{}` to the protection
// of production would unprotect it. Members the schema does not name stay
// accepted, so clients may send more than a server knows. A body that is no
// JSON is left to its own decoder, which refuses it in its own words.
func refuseMissingMembers(body []byte, op operations.Entry) error {
	if op.Body == 0 || len(bytes.TrimSpace(body)) == 0 || !json.Valid(body) {
		return nil
	}
	root := operations.Shapes[op.Body]
	var found []apperrors.Violation
	add := func(pointer, detail string) {
		if len(found) < text.MaxViolations {
			found = append(found, apperrors.Violation{Pointer: pointer, Code: "required", Detail: detail})
		}
	}
	if root.Object && bytes.TrimSpace(body)[0] != '{' {
		return apperrors.Invalid(bodyInvalid, "the request body must be a JSON object",
			apperrors.Violation{Pointer: "", Code: "invalid_type", Detail: "the body must be a JSON object"})
	}
	if root.Deep {
		checkShape(json.RawMessage(body), root, "", add)
	}
	if len(found) == 0 {
		return nil
	}
	return apperrors.Invalid(bodyInvalid, "the request body lacks what the operation requires", found...)
}

// checkShape reports, through add, what raw lacks of what shape requires,
// looking into the members, elements and values that hold more to check. A
// value of another type than its shape says is left to the decoder.
func checkShape(raw json.RawMessage, shape operations.Shape, pointer string, add func(pointer, detail string)) {
	switch first(raw) {
	case '{':
		var members map[string]json.RawMessage
		if json.Unmarshal(raw, &members) != nil {
			return
		}
		nullable := map[string]bool{}
		for _, m := range shape.Members {
			nullable[m.Name] = m.Nullable
		}
		for _, name := range shape.Required {
			v, present := members[name]
			switch {
			case !present:
				add(pointer+"/"+pointerToken.Replace(name), "the member is required")
			case isNull(v) && !nullable[name]:
				add(pointer+"/"+pointerToken.Replace(name), "the member is required and must not be null")
			}
		}
		for _, m := range shape.Members {
			if v, ok := members[m.Name]; ok && m.Shape != 0 {
				checkShape(v, operations.Shapes[m.Shape], pointer+"/"+pointerToken.Replace(m.Name), add)
			}
		}
		if shape.Values != 0 {
			named := map[string]bool{}
			for _, m := range shape.Members {
				named[m.Name] = true
			}
			for name, v := range members {
				if !named[name] {
					checkShape(v, operations.Shapes[shape.Values], pointer+"/"+pointerToken.Replace(name), add)
				}
			}
		}
	case '[':
		if shape.Items == 0 {
			return
		}
		var elements []json.RawMessage
		if json.Unmarshal(raw, &elements) != nil {
			return
		}
		for i, e := range elements {
			checkShape(e, operations.Shapes[shape.Items], pointer+"/"+strconv.Itoa(i), add)
		}
	}
}

// first returns the first byte of the JSON value raw, 0 for none.
func first(raw json.RawMessage) byte {
	if t := bytes.TrimSpace(raw); len(t) > 0 {
		return t[0]
	}
	return 0
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }
