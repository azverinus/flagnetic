package openfeature

import (
	"container/list"
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
//
// A unit evaluated again moves to the newest end, so a unit still in use is
// not pushed out by units first seen after it.
type lastContexts struct {
	mu    sync.Mutex
	byKey map[string]*list.Element
	order *list.List // of lastContext, oldest at the front
}

type lastContext struct {
	key string
	fc  flagnetic.Context
}

func newLastContexts() *lastContexts {
	return &lastContexts{byKey: map[string]*list.Element{}, order: list.New()}
}

func (l *lastContexts) remember(flag string, fc flagnetic.Context) {
	key := lastContextKey(flag, fc.Units)
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.byKey[key]; ok {
		e.Value = lastContext{key: key, fc: fc}
		l.order.MoveToBack(e)
		return
	}
	if l.order.Len() >= lastContextCapacity {
		oldest := l.order.Front()
		delete(l.byKey, oldest.Value.(lastContext).key)
		l.order.Remove(oldest)
	}
	l.byKey[key] = l.order.PushBack(lastContext{key: key, fc: fc})
}

// forget drops what was remembered, so an exposure for a flag that no
// longer carries an experiment block falls back to the tracked context.
func (l *lastContexts) forget(flag string, units map[string]string) {
	key := lastContextKey(flag, units)
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.byKey[key]; ok {
		delete(l.byKey, key)
		l.order.Remove(e)
	}
}

// recall returns the context the flag was last evaluated with for these
// units, or fallback when none is remembered.
func (l *lastContexts) recall(flag string, fallback flagnetic.Context) flagnetic.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.byKey[lastContextKey(flag, fallback.Units)]; ok {
		return e.Value.(lastContext).fc
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
