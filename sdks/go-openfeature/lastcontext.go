package openfeature

import (
	"sort"
	"strconv"
	"strings"
	"sync"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// lastContextCapacity bounds the evaluations the provider remembers; the
// oldest goes out first.
const lastContextCapacity = 10000

// lastContexts remembers, per flag and units, the flagnetic Context the
// provider last evaluated an experiment flag with. The OpenFeature SDK runs
// no hooks for Track, so the context Track receives lacks what a before
// hook added to the evaluation, and the client would find no detail under
// it; the remembered context is the one the detail is filed under.
type lastContexts struct {
	mu    sync.Mutex
	byKey map[string]flagnetic.Context
	order []string
}

func newLastContexts() *lastContexts {
	return &lastContexts{byKey: map[string]flagnetic.Context{}}
}

func (l *lastContexts) remember(flag string, fc flagnetic.Context) {
	key := lastContextKey(flag, fc.Units)
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.byKey[key]; !ok {
		if len(l.order) >= lastContextCapacity {
			delete(l.byKey, l.order[0])
			l.order = l.order[1:]
		}
		l.order = append(l.order, key)
	}
	l.byKey[key] = fc
}

// forget drops what was remembered, so an exposure for a flag that no
// longer carries an experiment block falls back to the tracked context.
func (l *lastContexts) forget(flag string, units map[string]string) {
	key := lastContextKey(flag, units)
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.byKey[key]; !ok {
		return
	}
	delete(l.byKey, key)
	for i, k := range l.order {
		if k == key {
			l.order = append(l.order[:i], l.order[i+1:]...)
			break
		}
	}
}

// recall returns the context the flag was last evaluated with for these
// units, or fallback when none is remembered.
func (l *lastContexts) recall(flag string, fallback flagnetic.Context) flagnetic.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	if fc, ok := l.byKey[lastContextKey(flag, fallback.Units)]; ok {
		return fc
	}
	return fallback
}

// lastContextKey writes every part with its length in front, so no flag key
// and no unit ID can forge a separator.
func lastContextKey(flag string, units map[string]string) string {
	var b strings.Builder
	write := func(s string) {
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
	}
	write(flag)
	types := make([]string, 0, len(units))
	for t := range units {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		write(t)
		write(units[t])
	}
	return b.String()
}
