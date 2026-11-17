package governance

import (
	"errors"
	"fmt"
	"testing"

	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func TestOnlyAnUnavailableFailureIsLeftUncountedByTheScheduler(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"a deadlock", apperrors.Unavailable("concurrent_change", "try again"), true},
		{"a deadlock wrapped", fmt.Errorf("applying: %w", apperrors.Unavailable("concurrent_change", "try again")), true},
		{"a refusal", apperrors.Invalid("invalid_configuration", "bad"), false},
		{"an internal error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryable(tt.err); got != tt.want {
				t.Errorf("retryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
