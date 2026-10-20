package flagnetictest_test

import (
	"context"
	"testing"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
	"github.com/azverinus/flagnetic/sdks/go/flagnetictest"
)

// checkoutPage stands for the code under test: it reads a flag through the
// client it is given.
func checkoutPage(ctx context.Context, c *flagnetic.Client, userID string) string {
	fc := flagnetic.Context{Units: map[string]string{"user": userID}}
	if c.Bool(ctx, fc, "new_checkout", false) {
		return "new"
	}
	return "old"
}

// A unit test exercises both branches of the code under test without a
// server: the test client serves each value as the flag's fallthrough, and
// Set rewrites a flag for the rest of the test.
func Example() {
	testCheckoutPage := func(t *testing.T) {
		c := flagnetictest.FromValues(t, map[string]any{"new_checkout": true})
		if got := checkoutPage(t.Context(), c.Client, "u-1"); got != "new" {
			t.Errorf("checkout page = %q with new_checkout on, want new", got)
		}

		c.Set("new_checkout", false)
		if got := checkoutPage(t.Context(), c.Client, "u-1"); got != "old" {
			t.Errorf("checkout page = %q with new_checkout off, want old", got)
		}
	}
	_ = testCheckoutPage // a test function of your package
}
