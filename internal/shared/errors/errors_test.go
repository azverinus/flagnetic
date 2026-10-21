package errors_test

import (
	stderrors "errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/errors"
)

func TestEveryConstructorGivesItsKindAndCode(t *testing.T) {
	tests := []struct {
		err  *errors.Error
		kind errors.Kind
		name string
	}{
		{errors.Invalid("flag_key_invalid", "bad key"), errors.KindInvalid, "invalid"},
		{errors.NotFound("flag_not_found", "no flag"), errors.KindNotFound, "not_found"},
		{errors.Conflict("flag_key_taken", "taken"), errors.KindConflict, "conflict"},
		{errors.Forbidden("permission_missing", "no"), errors.KindForbidden, "forbidden"},
		{errors.Unauthenticated("session_missing", "sign in"), errors.KindUnauthenticated, "unauthenticated"},
		{errors.PreconditionFailed("version_mismatch", "stale"), errors.KindPreconditionFailed, "precondition_failed"},
		{errors.Gone("invitation_expired", "expired"), errors.KindGone, "gone"},
		{errors.RateLimited("too_many_requests", "slow down", time.Second), errors.KindRateLimited, "rate_limited"},
		{errors.Unavailable("database_unavailable", "try again"), errors.KindUnavailable, "unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.Kind != tt.kind {
				t.Errorf("Kind = %v, want %v", tt.err.Kind, tt.kind)
			}
			if got := tt.err.Kind.String(); got != tt.name {
				t.Errorf("Kind.String() = %q, want %q", got, tt.name)
			}
			if tt.err.Code == "" || tt.err.Detail == "" {
				t.Errorf("code %q and detail %q must both be kept", tt.err.Code, tt.err.Detail)
			}
		})
	}
}

func TestKindAndCodeAreReadFromAWrappedChain(t *testing.T) {
	err := fmt.Errorf("loading flag: %w", errors.NotFound("flag_not_found", "no flag with key beta"))

	e, ok := errors.From(err)
	if !ok {
		t.Fatal("From found no kind in a wrapped chain")
	}
	if e.Kind != errors.KindNotFound || e.Code != "flag_not_found" {
		t.Errorf("From = %v %q, want not_found flag_not_found", e.Kind, e.Code)
	}
	var target *errors.Error
	if !stderrors.As(err, &target) {
		t.Error("errors.As does not find the kind")
	}
}

func TestAnErrorWithoutAKindHasNone(t *testing.T) {
	if _, ok := errors.From(io.EOF); ok {
		t.Error("From found a kind in a plain error")
	}
	if _, ok := errors.From(nil); ok {
		t.Error("From found a kind in nil")
	}
}

func TestCauseIsWrappedButNotInTheDetail(t *testing.T) {
	err := errors.Unavailable("database_unavailable", "the database did not answer").WithCause(io.ErrUnexpectedEOF)

	if !stderrors.Is(err, io.ErrUnexpectedEOF) {
		t.Error("the cause is not reachable through errors.Is")
	}
	if err.Detail != "the database did not answer" {
		t.Errorf("Detail = %q; the cause must not leak into it", err.Detail)
	}
	if got := err.Error(); got != "unavailable database_unavailable: the database did not answer: unexpected EOF" {
		t.Errorf("Error() = %q", got)
	}
}

func TestWithCauseLeavesTheOriginalUntouched(t *testing.T) {
	base := errors.NotFound("flag_not_found", "no flag")
	_ = base.WithCause(io.EOF).WithExtension("key", "beta")

	if base.Unwrap() != nil || base.Extensions != nil {
		t.Error("WithCause or WithExtension changed the error it was called on")
	}
}

func TestFieldViolationsAndExtensionsAreKept(t *testing.T) {
	err := errors.Invalid("request_invalid", "two fields are wrong",
		errors.Violation{Pointer: "/key", Code: "pattern", Detail: "must match ^[a-z]"},
		errors.Violation{Pointer: "/variations/1/value", Code: "type", Detail: "must be a boolean"},
	).WithExtension("references", []string{"segment beta"})

	if len(err.Violations) != 2 || err.Violations[1].Pointer != "/variations/1/value" {
		t.Errorf("Violations = %+v", err.Violations)
	}
	if got, ok := err.Extensions["references"].([]string); !ok || got[0] != "segment beta" {
		t.Errorf("Extensions = %+v", err.Extensions)
	}
}

func TestRateLimitedCarriesItsRetryDelay(t *testing.T) {
	err := fmt.Errorf("sign in: %w", errors.RateLimited("sign_in_locked", "too many attempts", 90*time.Second))

	e, _ := errors.From(err)
	if e.RetryAfter != 90*time.Second {
		t.Errorf("RetryAfter = %v, want 1m30s", e.RetryAfter)
	}
}

func TestAnUnavailableErrorCarriesARetryDelayWithoutChangingTheOriginal(t *testing.T) {
	busy := errors.Unavailable("busy", "no slot")
	e := busy.WithRetryAfter(time.Second)
	if e.RetryAfter != time.Second || busy.RetryAfter != 0 || e.Code != "busy" {
		t.Errorf("RetryAfter = %v and %v on the original, want 1s and none", e.RetryAfter, busy.RetryAfter)
	}
}

func TestIsKindAnswersForAWrappedChain(t *testing.T) {
	err := fmt.Errorf("x: %w", errors.Conflict("flag_key_taken", "taken"))
	if !errors.IsKind(err, errors.KindConflict) {
		t.Error("IsKind(conflict) = false")
	}
	if errors.IsKind(err, errors.KindNotFound) {
		t.Error("IsKind(not_found) = true")
	}
	if errors.IsKind(io.EOF, errors.KindConflict) {
		t.Error("IsKind on a plain error = true")
	}
}

func TestUnderPointsViolationsIntoALargerBodyWithoutChangingTheOriginal(t *testing.T) {
	orig := errors.Invalid("invalid_configuration", "bad", errors.Violation{Pointer: "/rules/0", Code: "too_many"},
		errors.Violation{Pointer: "", Code: "too_many"})
	under := orig.Under("/draft/configuration")
	if under.Violations[0].Pointer != "/draft/configuration/rules/0" || under.Violations[1].Pointer != "/draft/configuration" {
		t.Errorf("pointers = %+v", under.Violations)
	}
	if orig.Violations[0].Pointer != "/rules/0" {
		t.Errorf("the original changed: %+v", orig.Violations)
	}
}
