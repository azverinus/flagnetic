package sinks_test

import (
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	pgsinks "github.com/azverinus/flagnetic/internal/infra/out/postgres/sinks"
)

// A drop of the rows of an event kind that is no raw table is refused before
// the database is asked anything.
func TestAnEventKindWithNoRawTableIsRefusedBeforeTheDatabaseIsAsked(t *testing.T) {
	s := pgsinks.NewStore(nil)
	kind := sinks.EventKind("no_such_kind")
	days := []time.Time{time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)}
	if _, err := s.Lost(t.Context(), kind, sinks.Scope{}, days, events.Cursor{}); err == nil {
		t.Error("Lost accepted an event kind with no raw table")
	}
	if _, err := s.FirstStored(t.Context(), kind, sinks.Scope{}, days, events.Cursor{}); err == nil {
		t.Error("FirstStored accepted an event kind with no raw table")
	}
	if err := s.ExpireDeadLetters(t.Context(), kind, days[0]); err == nil {
		t.Error("ExpireDeadLetters accepted an event kind with no raw table")
	}
}
