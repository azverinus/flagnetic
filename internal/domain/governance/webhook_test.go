package governance_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

var projectEnvironments = []string{"development", "production"}

func checked(t *testing.T, in governance.WebhookInput, policy governance.TargetPolicy) (governance.WebhookInput, error) {
	t.Helper()
	return governance.CheckWebhook(in, projectEnvironments, policy)
}

func validInput() governance.WebhookInput {
	return governance.WebhookInput{Name: "SIEM", URL: "https://receiver.example/hook", Events: []string{"flag.kill_switch"}}
}

// problemOf returns the code of err and the pointers of its violations.
func problemOf(t *testing.T, err error) (string, []string) {
	t.Helper()
	var e *apperrors.Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v carries no kind", err)
	}
	pointers := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		pointers[i] = v.Pointer
	}
	return e.Code, pointers
}

func TestAWellFormedSubscriptionIsAcceptedTrimmedAndWithoutRepeats(t *testing.T) {
	in := validInput()
	in.Name = "  SIEM  "
	in.Events = []string{"flag.kill_switch", "*", "flag.kill_switch"}
	in.Environments = []string{"production", "production"}
	got, err := checked(t, in, governance.TargetPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "SIEM" || !slices.Equal(got.Events, []string{"flag.kill_switch", "*"}) ||
		!slices.Equal(got.Environments, []string{"production"}) {
		t.Errorf("checked = %+v", got)
	}
}

func TestAMalformedSubscriptionNamesEveryFieldAtOnce(t *testing.T) {
	for name, tc := range map[string]struct {
		in       governance.WebhookInput
		pointers []string
	}{
		"a blank name":           {governance.WebhookInput{Name: " ", URL: "https://r.example", Events: []string{"ping"}}, []string{"/name"}},
		"no events":              {governance.WebhookInput{Name: "a", URL: "https://r.example"}, []string{"/events"}},
		"an unknown environment": {governance.WebhookInput{Name: "a", URL: "https://r.example", Events: []string{"ping"}, Environments: []string{"staging"}}, []string{"/environments/0"}},
		"an unknown environment after a repeat": {governance.WebhookInput{Name: "a", URL: "https://r.example", Events: []string{"ping"},
			Environments: []string{"production", "production", "staging", "staging"}}, []string{"/environments/2"}},
		"not a URL":              {governance.WebhookInput{Name: "a", URL: "not a url", Events: []string{"ping"}}, []string{"/url"}},
		"another scheme":         {governance.WebhookInput{Name: "a", URL: "ftp://r.example/hook", Events: []string{"ping"}}, []string{"/url"}},
		"credentials in the URL": {governance.WebhookInput{Name: "a", URL: "https://u:p@r.example/hook", Events: []string{"ping"}}, []string{"/url"}},
		"a fragment":             {governance.WebhookInput{Name: "a", URL: "https://r.example/hook#x", Events: []string{"ping"}}, []string{"/url"}},
		"all of them":            {governance.WebhookInput{URL: "nope"}, []string{"/name", "/url", "/events"}},
	} {
		_, err := checked(t, tc.in, governance.TargetPolicy{})
		code, pointers := problemOf(t, err)
		if code != "invalid_webhook" || !slices.Equal(pointers, tc.pointers) {
			t.Errorf("%s: %s at %v, want invalid_webhook at %v", name, code, pointers, tc.pointers)
		}
	}
}

func TestAnEventTypeOutsideTheCatalogueIsRefused(t *testing.T) {
	in := validInput()
	in.Events = []string{"flag.kill_switch", "flag.exploded"}
	_, err := checked(t, in, governance.TargetPolicy{})
	if code, pointers := problemOf(t, err); code != "unknown_event_type" || !slices.Equal(pointers, []string{"/events/1"}) {
		t.Errorf("%s at %v, want unknown_event_type at /events/1", code, pointers)
	}
	// The pointer names the element as the request wrote it, repeats
	// included.
	in.Events = []string{"ping", "ping", "flag.exploded"}
	_, err = checked(t, in, governance.TargetPolicy{})
	if code, pointers := problemOf(t, err); code != "unknown_event_type" || !slices.Equal(pointers, []string{"/events/2"}) {
		t.Errorf("after a repeat: %s at %v, want unknown_event_type at /events/2", code, pointers)
	}
}

func TestPlainHTTPIsRefusedUnlessTheOperatorAllowsIt(t *testing.T) {
	in := validInput()
	in.URL = "http://receiver.example/hook"
	_, err := checked(t, in, governance.TargetPolicy{})
	if code, _ := problemOf(t, err); code != "insecure_url" {
		t.Errorf("code %s, want insecure_url", code)
	}
	if _, err := checked(t, in, governance.TargetPolicy{AllowHTTP: true}); err != nil {
		t.Errorf("allowed http refused: %v", err)
	}
}

func TestAnAddressOutsideThePublicInternetIsRefused(t *testing.T) {
	for addr, refused := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.0.0.1": true, "172.16.0.1": true, "192.168.1.1": true, "fd00::1": true,
		"169.254.169.254": true, "fe80::1": true, "0.0.0.0": true, "::": true, "224.0.0.1": true, "ff02::1": true,
		"::ffff:127.0.0.1": true, "93.184.216.34": false, "2606:2800:220:1::1": false,
	} {
		err := governance.CheckTargetAddresses([]netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr(addr)},
			governance.TargetPolicy{})
		if refused {
			if code, _ := problemOf(t, err); code != "private_network_target" {
				t.Errorf("%s: code %s, want private_network_target", addr, code)
			}
		} else if err != nil {
			t.Errorf("%s refused: %v", addr, err)
		}
		if err := governance.CheckTargetAddresses([]netip.Addr{netip.MustParseAddr(addr)},
			governance.TargetPolicy{AllowPrivateNetworks: true}); err != nil {
			t.Errorf("%s refused with private networks allowed: %v", addr, err)
		}
	}
}

func TestStarMatchesEveryTypeButTheAuditEntry(t *testing.T) {
	for _, tc := range []struct {
		events []string
		typ    string
		want   bool
	}{
		{[]string{"*"}, "flag.created", true},
		{[]string{"*"}, "audit.entry", false},
		{[]string{"*", "audit.entry"}, "audit.entry", true},
		{[]string{"flag.created"}, "flag.updated", false},
	} {
		if got := governance.WantsEventType(tc.events, tc.typ); got != tc.want {
			t.Errorf("%v wants %s = %t, want %t", tc.events, tc.typ, got, tc.want)
		}
	}
}

func TestTheCatalogueListsEachTypeOnceWithWhatEmitsIt(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range governance.EventCatalogue() {
		if seen[e.Type] || e.Description == "" || e.Type == "*" {
			t.Errorf("catalogue entry %+v", e)
		}
		seen[e.Type] = true
	}
	for _, typ := range []string{"flag.kill_switch", "grant.created", "audit.entry", "ping"} {
		if !seen[typ] {
			t.Errorf("the catalogue lacks %s", typ)
		}
	}
}

func TestASecretIsThirtyTwoRandomBytesInBase64URL(t *testing.T) {
	random := bytes.NewReader(bytes.Repeat([]byte{7}, 32))
	s, err := governance.NewWebhookSecret(random)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(s.Reveal())
	if err != nil || !bytes.Equal(raw, bytes.Repeat([]byte{7}, 32)) {
		t.Errorf("secret %q decodes to %v, %v", s.Reveal(), raw, err)
	}
}

func TestAChangeAppliesOnlyWhatItNames(t *testing.T) {
	in := validInput()
	in.Enabled = true
	name := "Security"
	got := in.With(governance.WebhookChange{Name: &name})
	if got.Name != "Security" || got.URL != in.URL || !got.Enabled || !slices.Equal(got.Events, in.Events) {
		t.Errorf("with a new name = %+v", got)
	}
	if !in.Same(in.With(governance.WebhookChange{})) || in.Same(got) {
		t.Error("Same tells an unchanged input from a changed one wrongly")
	}
}
