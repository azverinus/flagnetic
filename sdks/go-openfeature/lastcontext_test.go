package openfeature

import (
	"strconv"
	"testing"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

func TestLastContextsAreBoundedOldestOut(t *testing.T) {
	l := newLastContexts()
	for i := 0; i <= lastContextCapacity; i++ {
		l.remember("flag", flagnetic.Context{Units: map[string]string{"user": strconv.Itoa(i)}})
	}
	if len(l.byKey) != lastContextCapacity {
		t.Fatalf("remembered = %d, want %d", len(l.byKey), lastContextCapacity)
	}
	fallback := flagnetic.Context{Units: map[string]string{"user": "0"}, Attributes: map[string]any{"fallback": true}}
	if got := l.recall("flag", fallback); got.Attributes["fallback"] != true {
		t.Errorf("the oldest unit was kept: %+v", got)
	}
	newest := flagnetic.Context{Units: map[string]string{"user": strconv.Itoa(lastContextCapacity)}, Attributes: map[string]any{"fallback": true}}
	if got := l.recall("flag", newest); got.Attributes != nil {
		t.Errorf("the newest unit was lost: %+v", got)
	}
}

func TestLastContextsForgetFallsBack(t *testing.T) {
	l := newLastContexts()
	units := map[string]string{"user": "u-1"}
	l.remember("flag", flagnetic.Context{Units: units, Attributes: map[string]any{"a": 1}})
	l.forget("flag", units)
	l.forget("flag", units)
	fallback := flagnetic.Context{Units: units}
	if got := l.recall("flag", fallback); got.Attributes != nil {
		t.Errorf("recall after forget = %+v, want the fallback", got)
	}
}
