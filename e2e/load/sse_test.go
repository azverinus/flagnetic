package load

import (
	"slices"
	"strings"
	"testing"
)

func TestReadVersions(t *testing.T) {
	const stream = "retry: 3000\n\n" +
		"event: ruleset\nid: 1745040000000\ndata: {\"version\":1745040000000}\n\n" +
		": hb\n\n" +
		"event: other\ndata: {\"version\":9}\n\n" +
		"event: ruleset\nid: 1745040000001\ndata: {\"version\":1745040000001}\n\n" +
		"event: ruleset\ndata: not json\n\n"
	var got []int64
	if err := ReadVersions(strings.NewReader(stream), func(v int64) { got = append(got, v) }); err != nil {
		t.Fatal(err)
	}
	if want := []int64{1745040000000, 1745040000001}; !slices.Equal(got, want) {
		t.Errorf("versions = %v, want %v", got, want)
	}
}
