package flagnetic

// generations is a bounded map whose entries survive one rotation: reads
// see the current generation and the one before it, and a rotation forgets
// only the older one. It bounds memory without a timer and without the
// bookkeeping of an eviction order, which is what a suppression set the
// platform does not rely on needs.
//
// It holds no clock and no window: when a generation has lived long enough
// is the owner's to decide, and it calls expire. A full generation rotates
// on its own, because the bound is the whole point.
//
// It is not safe for concurrent use; its owner holds a mutex over it.
type generations[K comparable, V any] struct {
	capacity int
	current  map[K]V
	previous map[K]V
}

func newGenerations[K comparable, V any](capacity int) *generations[K, V] {
	return &generations[K, V]{
		capacity: capacity,
		current:  map[K]V{},
		previous: map[K]V{},
	}
}

// load returns what is held for k in either generation.
func (g *generations[K, V]) load(k K) (V, bool) {
	if v, ok := g.current[k]; ok {
		return v, true
	}
	v, ok := g.previous[k]
	return v, ok
}

// store writes k into the current generation, expiring the older one first
// when the current one is full.
func (g *generations[K, V]) store(k K, v V) {
	if _, held := g.current[k]; !held && len(g.current) >= g.capacity {
		g.expire()
	}
	g.current[k] = v
}

// delete forgets k in both generations.
func (g *generations[K, V]) delete(k K) {
	delete(g.current, k)
	delete(g.previous, k)
}

// expire forgets the older generation and makes the current one older.
// Every entry therefore survives at least until the expire after the one
// that followed its store.
func (g *generations[K, V]) expire() {
	g.previous, g.current = g.current, map[K]V{}
}
