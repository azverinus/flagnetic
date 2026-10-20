package flagnetic

import "testing"

// The bounded map behind the suppression set and the detail lookup: an
// entry survives one expiry, and neither generation ever grows past the
// capacity.

func TestAGenerationSurvivesOneExpiry(t *testing.T) {
	g := newGenerations[string, int](4)
	g.store("a", 1)

	if v, ok := g.load("a"); !ok || v != 1 {
		t.Fatalf("a = %v, %v; want 1, true", v, ok)
	}
	// One expiry on, the entry has moved to the older generation and is
	// still read: it lives for at least the window its owner keeps.
	g.expire()
	if v, ok := g.load("a"); !ok || v != 1 {
		t.Fatalf("after one expiry a = %v, %v; want 1, true", v, ok)
	}
	// A second forgets it.
	g.expire()
	if _, ok := g.load("a"); ok {
		t.Error("after two expiries a is still held")
	}
}

func TestGenerationsAreBoundedByCapacity(t *testing.T) {
	const capacity = 8
	g := newGenerations[int, struct{}](capacity)
	for i := range 100 * capacity {
		g.store(i, struct{}{})
	}
	if held := len(g.current) + len(g.previous); held > 2*capacity {
		t.Errorf("%d keys held, want at most twice the capacity of %d", held, capacity)
	}
	// The newest key survives the expiries the flood caused.
	if _, ok := g.load(100*capacity - 1); !ok {
		t.Error("the newest key was dropped")
	}
}

func TestDeleteForgetsBothGenerations(t *testing.T) {
	g := newGenerations[string, int](4)
	g.store("a", 1)
	g.expire()
	g.store("a", 2)
	g.delete("a")
	if _, ok := g.load("a"); ok {
		t.Error("a is still held in one of the generations")
	}
}
