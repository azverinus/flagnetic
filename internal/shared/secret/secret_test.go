package secret_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/azverinus/flagnetic/internal/shared/secret"
)

const value = "hunter2"

func TestASecretIsRedactedInEveryLogLine(t *testing.T) {
	s := secret.New(value)
	for _, newHandler := range []func(*bytes.Buffer) slog.Handler{
		func(b *bytes.Buffer) slog.Handler { return slog.NewJSONHandler(b, nil) },
		func(b *bytes.Buffer) slog.Handler { return slog.NewTextHandler(b, nil) },
	} {
		var buf bytes.Buffer
		slog.New(newHandler(&buf)).Info("configured", "password", s, "nested", slog.GroupValue(slog.Any("key", s)))
		if strings.Contains(buf.String(), value) || !strings.Contains(buf.String(), "[redacted]") {
			t.Errorf("log line = %s", buf.String())
		}
	}
}

func TestASecretIsRedactedWhenFormatted(t *testing.T) {
	s := secret.New(value)
	type config struct {
		Password secret.String
	}
	for _, got := range []string{
		s.String(),
		fmt.Sprint(s),
		fmt.Sprintf("%v %s %q %+v %#v", s, s, s, config{s}, s),
	} {
		if strings.Contains(got, value) {
			t.Errorf("formatted = %q", got)
		}
	}
	b, err := json.Marshal(config{s})
	if err != nil || strings.Contains(string(b), value) {
		t.Errorf("json = %s, %v", b, err)
	}
}

func TestRevealGivesTheValue(t *testing.T) {
	if got := secret.New(value).Reveal(); got != value {
		t.Errorf("Reveal = %q", got)
	}
	var zero secret.String
	if !zero.IsZero() || secret.New(value).IsZero() {
		t.Error("IsZero is wrong")
	}
}
