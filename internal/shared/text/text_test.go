package text_test

import (
	"net/url"
	"reflect"
	"testing"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
	"github.com/azverinus/flagnetic/internal/shared/text"
)

func TestAllowedKeepsOrdinaryTextWithLineBreaksAndTabs(t *testing.T) {
	for _, s := range []string{"", "plain", "line one\nline two\r\n\tindented", "ünï 日本 😀", " "} {
		if !text.Allowed(s) {
			t.Errorf("Allowed(%q) = false, want true", s)
		}
	}
}

func TestAllowedRefusesControlCharacters(t *testing.T) {
	for _, s := range []string{"\x00", "a\x00b", "\x01", "\x1b[0m", "\x7f", "\u0085"} {
		if text.Allowed(s) {
			t.Errorf("Allowed(%q) = true, want false", s)
		}
	}
}

func TestInJSONNamesEveryRefusedStringAndNameByPointer(t *testing.T) {
	body := `{"name":"ok","note":"a\u0000b","list":["fine",{"deep":"\u0001"}],"k\u0000ey":1,"a/b":"x\u0000"}`
	got := text.InJSON([]byte(body))
	var pointers []string
	for _, v := range got {
		if v.Code != "invalid_character" {
			t.Errorf("violation %+v: code, want invalid_character", v)
		}
		pointers = append(pointers, v.Pointer)
	}
	want := []string{"/note", "/list/1/deep", "/k\u0000ey", "/a~1b"}
	if !reflect.DeepEqual(pointers, want) {
		t.Errorf("pointers = %q, want %q", pointers, want)
	}
}

func TestInJSONLeavesAMalformedBodyToItsDecoder(t *testing.T) {
	for _, body := range []string{"", "{", `{"a":"\u0000"`, `nope`} {
		if got := text.InJSON([]byte(body)); len(got) > 1 {
			t.Errorf("InJSON(%q) = %v", body, got)
		}
	}
}

func TestInTreeWalksADecodedDocument(t *testing.T) {
	tree := map[string]any{"a": []any{"x", "y\x00"}, "b": map[string]any{"c": "d\x00"}, "n": 3}
	got := text.InTree(tree)
	if len(got) != 2 || got[0].Pointer != "/a/1" || got[1].Pointer != "/b/c" {
		t.Errorf("InTree = %+v, want /a/1 and /b/c", got)
	}
}

func TestViolationsAreBounded(t *testing.T) {
	var tree []any
	for range 500 {
		tree = append(tree, "\x00")
	}
	if got := text.InTree(tree); len(got) != text.MaxViolations {
		t.Errorf("violations = %d, want %d", len(got), text.MaxViolations)
	}
}

func TestErrorIsInvalidWithItsViolations(t *testing.T) {
	if text.Error(nil) != nil {
		t.Error("Error(nil) must be nil")
	}
	err := text.Error(text.InTree("\x00"))
	var e *apperrors.Error
	if err == nil || !asError(err, &e) || e.Kind != apperrors.KindInvalid || e.Code != "invalid_character" || len(e.Violations) != 1 {
		t.Errorf("Error = %#v", err)
	}
}

func asError(err error, target **apperrors.Error) bool {
	e, ok := err.(*apperrors.Error)
	*target = e
	return ok
}

func TestAllowedURLReadsThePathAndQueryDecoded(t *testing.T) {
	for raw, want := range map[string]bool{
		"/a/b?q=x&tag=y%20z": true, "/a%00b": false, "/a?q=%00": false, "/a?%01=x": false, "/a?q=line%0Abreak": true,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := text.AllowedURL(u); got != want {
			t.Errorf("AllowedURL(%q) = %v, want %v", raw, got, want)
		}
	}
}
