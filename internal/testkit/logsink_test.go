package testkit_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestLogSinkCapturesStructuredRecords(t *testing.T) {
	sink := testkit.NewLogSink()
	log := sink.Logger().With("request_id", "r1").WithGroup("http")
	log.Debug("request", "status", 200, "duration", time.Second)
	sink.Logger().Warn("other")

	got := sink.Find("request")
	if len(got) != 1 {
		t.Fatalf("Find(request) = %d records, want 1", len(got))
	}
	r := got[0]
	if r.Level != slog.LevelDebug {
		t.Errorf("Level = %v", r.Level)
	}
	if r.Attrs["request_id"] != "r1" || r.Attrs["http.status"] != int64(200) || r.Attrs["http.duration"] != time.Second {
		t.Errorf("Attrs = %v", r.Attrs)
	}
	if n := len(sink.Records()); n != 2 {
		t.Errorf("Records = %d, want 2", n)
	}
}
