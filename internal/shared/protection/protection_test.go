package protection_test

import (
	"context"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/protection"
)

type environment struct{}

func TestABypassLetsThroughWhatItNamesAndNothingElse(t *testing.T) {
	src := id.NewDeterministicSource(clock.NewFake(time.UnixMilli(1469918264193)))
	production, staging := id.New[environment](src), id.New[environment](src)
	ctx := t.Context()

	if protection.Bypassed(ctx, production) {
		t.Error("a context without a bypass lets production through")
	}
	everywhere := protection.BypassEverywhere(ctx)
	if !protection.Bypassed(everywhere, production) || !protection.Bypassed(everywhere, staging) {
		t.Error("the kill switch's bypass does not let every environment through")
	}
	covered := protection.Bypass(ctx, production)
	if !protection.Bypassed(covered, production) || protection.Bypassed(covered, staging) {
		t.Error("a change request's bypass lets through other than the environments it covers")
	}
	if protection.Bypassed(context.WithoutCancel(protection.Bypass[environment](ctx)), production) {
		t.Error("a bypass of no environment lets production through")
	}
}
