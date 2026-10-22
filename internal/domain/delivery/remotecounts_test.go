package delivery_test

import (
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
)

func TestTheUserAgentNamesTheSDK(t *testing.T) {
	for ua, want := range map[string]delivery.SDKIdentity{
		"flagnetic-go/1.4.0 (linux)":      {Name: "flagnetic-go", Version: "1.4.0", Platform: "linux"},
		"flagnetic-js/1.2.0 (web)":        {Name: "flagnetic-js", Version: "1.2.0", Platform: "web"},
		"flagnetic-dart/0.1.0":            {Name: "flagnetic-dart", Version: "0.1.0"},
		"Mozilla/5.0 (X11; Linux x86_64)": {Name: "unknown"},
		"":                                {Name: "unknown"},
		"curl/8.4.0":                      {Name: "unknown"},
		"flagnetic-go/" + strings.Repeat("9", 300): {Name: "flagnetic-go", Version: strings.Repeat("9", 256)},
	} {
		if got := delivery.ParseUserAgent(ua); got != want {
			t.Errorf("ParseUserAgent(%q) = %+v, want %+v", ua, got, want)
		}
	}
}

func TestRemoteEvaluationsAreCountedByTheirTrueReason(t *testing.T) {
	key := delivery.SDKKey{Kind: delivery.SDKKeyClient}
	web := delivery.RemoteEvaluator{Key: key, SDK: delivery.SDKIdentity{Name: "flagnetic-js", Version: "1.2.0", Platform: "web"}, AppVersion: "3.1.0"}
	at := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	var c delivery.RemoteCounts
	c.Add(web, 5, map[string]eval.Result{
		"checkout": {Variation: "true", Reason: eval.ReasonRule},
		"banner":   {Variation: "on", Reason: eval.ReasonFallthrough},
	}, at.Add(10*time.Second))
	c.Add(web, 7, map[string]eval.Result{"checkout": {Variation: "true", Reason: eval.ReasonRule}}, at)
	other := web
	other.AppVersion = "3.2.0"
	c.Add(other, 7, map[string]eval.Result{"checkout": {Variation: "true", Reason: eval.ReasonRule}}, at)

	flushed := at.Add(time.Minute)
	got := map[string]delivery.SummaryRecord{}
	for _, r := range c.Records(flushed) {
		got[r.Row.Flag+"/"+r.AppVersion] = r
	}
	if len(got) != 3 {
		t.Fatalf("records = %+v, want checkout per app version and banner", got)
	}
	checkout := got["checkout/3.1.0"]
	if checkout.Row.Count != 2 || checkout.Row.Reason != "rule" || checkout.RulesetVersion != 7 || !checkout.PeriodStart.Equal(at) ||
		!checkout.PeriodEnd.Equal(flushed) || !checkout.ReceivedAt.Equal(flushed) || checkout.Source != delivery.SourceRemoteEvaluation ||
		checkout.SDK != web.SDK || !checkout.EventID.IsZero() {
		t.Errorf("checkout = %+v", checkout)
	}
	if b := got["banner/3.1.0"]; b.Row.Count != 1 || b.Row.Reason != "fallthrough" || b.RulesetVersion != 5 {
		t.Errorf("banner = %+v", b)
	}
	if o := got["checkout/3.2.0"]; o.Row.Count != 1 {
		t.Errorf("another app version = %+v", o)
	}
}

func TestRemoteCountsAreBounded(t *testing.T) {
	var c delivery.RemoteCounts
	e := delivery.RemoteEvaluator{SDK: delivery.SDKIdentity{Name: "unknown"}}
	results := map[string]eval.Result{}
	for i := range delivery.MaxRemoteCountGroups + 3 {
		results["flag"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+time.Duration(i).String()] = eval.Result{Reason: eval.ReasonOff}
	}
	c.Add(e, 1, results, time.Time{})
	if c.Len() != delivery.MaxRemoteCountGroups || c.Dropped != 3 {
		t.Errorf("groups = %d, dropped = %d, want %d and 3", c.Len(), c.Dropped, delivery.MaxRemoteCountGroups)
	}
}
