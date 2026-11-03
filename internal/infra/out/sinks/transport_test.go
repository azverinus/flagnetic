package sinks_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	domain "github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/infra/out/sinks"
)

// resolver answers names from a table, as a DNS server pointing them anywhere.
type resolver map[string][]netip.Addr

func (r resolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if addrs, ok := r[host]; ok {
		return addrs, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

// target serves answer on loopback and counts the calls it gets.
func target(t *testing.T, answer http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		answer(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

// call makes a GET to url with c and returns its answer or the failure of getting none.
func call(t *testing.T, c *http.Client, url string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Do(req)
	if res != nil {
		t.Cleanup(func() { _ = res.Body.Close() })
	}
	return res, err
}

func TestAWebhookSinkIsRefusedLoopbackUnlessTheOperatorAllowsPrivateNetworks(t *testing.T) {
	srv, calls := target(t, ok)
	_, err := call(t, sinks.NewClient(domain.KindWebhook, sinks.Options{}), srv.URL)
	if got := sinks.ClassifyError(err); got.Class != domain.ClassRefusedTarget {
		t.Fatalf("a loopback receiver: %v, class %q, want refused_target", err, got.Class)
	}
	if calls.Load() != 0 {
		t.Errorf("the receiver was called %d times", calls.Load())
	}
	res, err := call(t, sinks.NewClient(domain.KindWebhook, sinks.Options{Policy: governance.TargetPolicy{AllowPrivateNetworks: true}}), srv.URL)
	if err != nil || res.StatusCode != http.StatusNoContent {
		t.Errorf("a loopback receiver with private networks allowed: %v, %v", res, err)
	}
}

func TestAWebhookSinkIsRefusedANamePointedAtAPrivateAddress(t *testing.T) {
	srv, calls := target(t, ok)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	names := resolver{"hooks.example": {netip.MustParseAddr("10.0.0.5"), netip.MustParseAddr("127.0.0.1")}}
	_, err := call(t, sinks.NewClient(domain.KindWebhook, sinks.Options{Resolver: names}), "http://hooks.example:"+port)
	if got := sinks.ClassifyError(err); got.Class != domain.ClassRefusedTarget {
		t.Errorf("a name pointed at private addresses: %v, class %q", err, got.Class)
	}
	if calls.Load() != 0 {
		t.Errorf("the receiver was called %d times", calls.Load())
	}
}

func TestANameIsJudgedAgainOnEveryDialWhateverItResolvedToBefore(t *testing.T) {
	srv, calls := target(t, ok)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	names := resolver{"warehouse.example": {netip.MustParseAddr("127.0.0.1")}}
	c := sinks.NewClient(domain.KindClickHouse, sinks.Options{Resolver: names})
	if res, err := call(t, c, "http://warehouse.example:"+port); err != nil || res.StatusCode != http.StatusNoContent {
		t.Fatalf("the name pointed at loopback: %v, %v", res, err)
	}
	names["warehouse.example"] = []netip.Addr{netip.MustParseAddr("169.254.169.254")}
	c.CloseIdleConnections()
	_, err := call(t, c, "http://warehouse.example:"+port)
	if got := sinks.ClassifyError(err); got.Class != domain.ClassRefusedTarget {
		t.Errorf("the name pointed at a link-local address afterwards: %v, class %q", err, got.Class)
	}
	if calls.Load() != 1 {
		t.Errorf("the target was called %d times, want once", calls.Load())
	}
}

func TestClickHouseAndObjectStorageMayReachPrivateAddressesAndNeverLinkLocalOnes(t *testing.T) {
	srv, calls := target(t, ok)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	names := resolver{
		"inside.example":   {netip.MustParseAddr("127.0.0.1")},
		"metadata.example": {netip.MustParseAddr("169.254.169.254")},
		"v6meta.example":   {netip.MustParseAddr("fe80::1")},
		"mixed.example":    {netip.MustParseAddr("169.254.169.254"), netip.MustParseAddr("127.0.0.1")},
	}
	for _, k := range []domain.Kind{domain.KindClickHouse, domain.KindObjectStorage} {
		// The operator's setting lifts nothing for them.
		for _, policy := range []governance.TargetPolicy{{}, {AllowPrivateNetworks: true, AllowHTTP: true}} {
			c := sinks.NewClient(k, sinks.Options{Resolver: names, Policy: policy})
			for _, host := range []string{"inside.example", "127.0.0.1", "mixed.example"} {
				before := calls.Load()
				res, err := call(t, c, "http://"+host+":"+port)
				if err != nil || res.StatusCode != http.StatusNoContent || calls.Load() != before+1 {
					t.Errorf("%s reaching %s: %v, %v", k, host, res, err)
				}
			}
			for _, host := range []string{"metadata.example", "v6meta.example", "169.254.169.254", "[fe80::1]", "[::ffff:169.254.169.254]", "0.0.0.0", "224.0.0.1"} {
				before := calls.Load()
				_, err := call(t, c, "http://"+host+":"+port)
				if got := sinks.ClassifyError(err); got.Class != domain.ClassRefusedTarget {
					t.Errorf("%s reaching %s: %v, class %q, want refused_target", k, host, err, got.Class)
				}
				if calls.Load() != before {
					t.Errorf("%s: %s was called", k, host)
				}
			}
		}
	}
}

func TestNoSinkFollowsARedirectOrUsesAProxy(t *testing.T) {
	elsewhere, reached := target(t, ok)
	srv, _ := target(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	})
	for _, k := range domain.Kinds {
		c := sinks.NewClient(k, sinks.Options{Policy: governance.TargetPolicy{AllowPrivateNetworks: true}})
		res, err := call(t, c, srv.URL)
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if res.StatusCode != http.StatusFound {
			t.Errorf("%s: status %d, want the redirect itself", k, res.StatusCode)
		}
		failure, failed := sinks.ClassifyResponse(k, res)
		if !failed || failure != (domain.Failure{Class: domain.ClassRedirect, Status: http.StatusFound}) {
			t.Errorf("%s: %+v, %t, want a redirect", k, failure, failed)
		}
		if reached.Load() != 0 {
			t.Errorf("%s followed the redirect", k)
		}
		if tr, isTransport := c.Transport.(*http.Transport); !isTransport || tr.Proxy != nil {
			t.Errorf("%s: transport %#v may use a proxy", k, c.Transport)
		}
	}
}

func TestEachKindIsBoundedByItsOwnTimeout(t *testing.T) {
	for k, want := range map[domain.Kind]time.Duration{
		domain.KindWebhook: 10 * time.Second, domain.KindClickHouse: time.Minute, domain.KindObjectStorage: 2 * time.Minute,
	} {
		if got := sinks.NewClient(k, sinks.Options{}).Timeout; got != want {
			t.Errorf("%s: timeout %s, want %s", k, got, want)
		}
	}
}

func TestATransportErrorIsReducedToAClassWithoutItsText(t *testing.T) {
	open, _ := target(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(ok))
	t.Cleanup(tlsServer.Close)
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedURL := "http://" + closed.Addr().String()
	_ = closed.Close()

	allowed := governance.TargetPolicy{AllowPrivateNetworks: true}
	for _, tc := range []struct {
		name   string
		client *http.Client
		url    string
		want   domain.FailureClass
	}{
		{"an unknown name", sinks.NewClient(domain.KindClickHouse, sinks.Options{Resolver: resolver{}}), "http://nowhere.example:9", domain.ClassDNS},
		{"a closed port", sinks.NewClient(domain.KindClickHouse, sinks.Options{}), closedURL, domain.ClassConnect},
		{"a certificate nobody vouches for", sinks.NewClient(domain.KindClickHouse, sinks.Options{}), tlsServer.URL, domain.ClassTLS},
		{"a plain server answering a TLS client", sinks.NewClient(domain.KindClickHouse, sinks.Options{}), strings.Replace(open.URL, "http:", "https:", 1), domain.ClassTLS},
		{"a webhook receiver's certificate", sinks.NewClient(domain.KindWebhook, sinks.Options{Policy: allowed}), tlsServer.URL, domain.ClassTLS},
		{"an answer that never comes", func() *http.Client {
			c := sinks.NewClient(domain.KindClickHouse, sinks.Options{})
			c.Timeout = 100 * time.Millisecond
			return c
		}(), open.URL, domain.ClassTimeout},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := call(t, tc.client, tc.url)
			if err == nil {
				t.Fatal("the call succeeded")
			}
			got := sinks.ClassifyError(err)
			if got.Class != tc.want || got.Status != 0 || got.Code != "" {
				t.Errorf("%v: %+v, want class %q alone", err, got, tc.want)
			}
		})
	}
}

func TestOnlyAClickHouseSinkMayAskNotToVerifyACertificate(t *testing.T) {
	tlsServer := httptest.NewTLSServer(http.HandlerFunc(ok))
	t.Cleanup(tlsServer.Close)
	res, err := call(t, sinks.NewClient(domain.KindClickHouse, sinks.Options{TLSSkipVerify: true}), tlsServer.URL)
	if err != nil || res.StatusCode != http.StatusNoContent {
		t.Errorf("a ClickHouse sink skipping verification: %v, %v", res, err)
	}
	for _, k := range []domain.Kind{domain.KindObjectStorage, domain.KindWebhook} {
		_, err := call(t, sinks.NewClient(k, sinks.Options{TLSSkipVerify: true, Policy: governance.TargetPolicy{AllowPrivateNetworks: true}}), tlsServer.URL)
		if got := sinks.ClassifyError(err); got.Class != domain.ClassTLS {
			t.Errorf("%s skipping verification: %v, class %q, want tls", k, err, got.Class)
		}
	}
}

func TestADeadlineIsATimeoutAndAnythingElseWithoutAnAnswerIsAConnectFailure(t *testing.T) {
	if got := sinks.ClassifyError(context.DeadlineExceeded); got.Class != domain.ClassTimeout {
		t.Errorf("a deadline: %q", got.Class)
	}
	if got := sinks.ClassifyError(errors.New("boom")); got.Class != domain.ClassConnect {
		t.Errorf("anything else: %q", got.Class)
	}
}
