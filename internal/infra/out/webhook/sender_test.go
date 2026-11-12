package webhook_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/out/webhook"
)

func TestAFailedCallIsKeptAsItsClassAndNoneOfTheNetworksWords(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	for name, tc := range map[string]struct {
		policy governance.TargetPolicy
		target string
		want   string
	}{
		"a loopback address":            {governance.TargetPolicy{}, closedURL, "refused_target"},
		"a carrier-grade NAT address":   {governance.TargetPolicy{}, "http://100.100.100.200/", "refused_target"},
		"a NAT64 address of a metadata": {governance.TargetPolicy{}, "http://[64:ff9b::a9fe:a9fe]/", "refused_target"},
		"a refused connection":          {governance.TargetPolicy{AllowPrivateNetworks: true}, closedURL, "connect"},
	} {
		t.Run(name, func(t *testing.T) {
			sent := webhook.NewSender(tc.policy).Send(context.Background(), tc.target, nil, []byte("{}"))
			if sent.Err == nil || sent.Err.Error() != tc.want {
				t.Errorf("error %v, want exactly %q", sent.Err, tc.want)
			}
		})
	}
}
