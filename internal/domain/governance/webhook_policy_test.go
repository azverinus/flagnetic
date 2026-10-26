package governance_test

import (
	"errors"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

var someWebhook = id.MustParse[governance.Webhook]("0190d2a0-0000-7000-8000-0000000000f5")

// failing returns an enabled subscription enabled at occurred that has
// failed failures times in a row since.
func failing(failures int) governance.Webhook {
	return governance.Webhook{
		ID: someWebhook, Project: someProject,
		WebhookInput:        governance.WebhookInput{Name: "SIEM", URL: "https://receiver.example/hook", Events: []string{"*"}, Enabled: true},
		ConsecutiveFailures: failures, EnabledAt: occurred, CreatedAt: occurred, UpdatedAt: occurred, Version: 3,
	}
}

func TestASubscriptionFailingTwentyFiveTimesWithNoSuccessForADayIsPersistentlyFailing(t *testing.T) {
	day := 24 * time.Hour
	success := occurred.Add(2 * time.Hour)
	for _, c := range []struct {
		name        string
		failures    int
		lastSuccess *time.Time
		at          time.Time
		want        bool
	}{
		{"both reached", 25, nil, occurred.Add(day), true},
		{"more failures, longer", 40, nil, occurred.Add(3 * day), true},
		{"one failure short", 24, nil, occurred.Add(3 * day), false},
		{"a day not yet over since the enable", 25, nil, occurred.Add(day - time.Second), false},
		{"a success within the day", 25, &success, occurred.Add(day), false},
		{"a day since the last success", 25, &success, success.Add(day), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := failing(c.failures)
			w.LastSuccessAt = c.lastSuccess
			if got := w.PersistentlyFailing(c.at); got != c.want {
				t.Errorf("PersistentlyFailing = %v, want %v", got, c.want)
			}
		})
	}
	disabled := failing(30)
	disabled.Enabled = false
	if disabled.PersistentlyFailing(occurred.Add(2 * day)) {
		t.Error("a disabled subscription is judged persistently failing again")
	}
}

func TestThePlatformDisablesASubscriptionWithItsReason(t *testing.T) {
	at := occurred.Add(25 * time.Hour)
	got := failing(25).DisableForFailure(at)
	if got.Enabled || got.DisabledReason != governance.DisabledPersistentFailure {
		t.Errorf("disabled %+v", got)
	}
	if got.Version != 4 || !got.UpdatedAt.Equal(at) || got.ConsecutiveFailures != 25 {
		t.Errorf("disabled %+v, want the next version at %v with its failures kept", got, at)
	}
}

func TestEnablingClearsTheReasonAndStartsTheWindowAgain(t *testing.T) {
	at := occurred.Add(48 * time.Hour)
	got, changed := failing(25).DisableForFailure(occurred.Add(25*time.Hour)).Enable(at)
	if !changed || !got.Enabled || got.DisabledReason != "" || got.ConsecutiveFailures != 0 || !got.EnabledAt.Equal(at) {
		t.Errorf("enabled %+v", got)
	}
	if got.Version != 5 || !got.UpdatedAt.Equal(at) {
		t.Errorf("enabled %+v, want the next version at %v", got, at)
	}
	if again, changed := got.Enable(at.Add(time.Hour)); changed || again.Version != got.Version {
		t.Errorf("enabling an enabled subscription changed it to %+v", again)
	}
}

func TestAnEditCannotEnableWhatThePlatformDisabled(t *testing.T) {
	disabled := failing(25).DisableForFailure(occurred.Add(25 * time.Hour))
	on, off, name := true, false, "Renamed"
	if err := disabled.CheckChange(governance.WebhookChange{Enabled: &on}); !errors.Is(err, governance.ErrPlatformDisabled) {
		t.Errorf("enabling by an edit: %v, want ErrPlatformDisabled", err)
	}
	for _, change := range []governance.WebhookChange{{Enabled: &off}, {Name: &name}} {
		if err := disabled.CheckChange(change); err != nil {
			t.Errorf("edit %+v: %v", change, err)
		}
	}
	memberDisabled := failing(0)
	memberDisabled.Enabled = false
	if err := memberDisabled.CheckChange(governance.WebhookChange{Enabled: &on}); err != nil {
		t.Errorf("enabling what a member disabled: %v", err)
	}
}

func TestARotationKeepsTheReplacedSecretSigningForTheOverlap(t *testing.T) {
	w := failing(0)
	w.SealedSecret = "first"
	at := occurred.Add(time.Hour)
	once := w.RotateSecret("second", at, 24*time.Hour)
	if once.SealedSecret != "second" || once.PreviousSealedSecret != "first" || once.PreviousSecretExpiresAt == nil ||
		!once.PreviousSecretExpiresAt.Equal(at.Add(24*time.Hour)) {
		t.Errorf("rotated %+v", once)
	}
	if once.Version != w.Version+1 || !once.UpdatedAt.Equal(at) {
		t.Errorf("rotated %+v, want the next version at %v", once, at)
	}
	twice := once.RotateSecret("third", at.Add(time.Minute), 24*time.Hour)
	if twice.SealedSecret != "third" || twice.PreviousSealedSecret != "second" {
		t.Errorf("rotated twice %+v, want only the last two secrets", twice)
	}
}

func TestOtherSubscriptionsAreToldWhyOneWasDisabledButNotWhere(t *testing.T) {
	w := failing(25)
	w.URL = "https://user:token@receiver.example/hook"
	at := occurred.Add(25 * time.Hour)
	for _, c := range []struct {
		name string
		last governance.DeliveryAttempt
		want string
	}{
		{"an answer", governance.DeliveryAttempt{N: 3, At: at, StatusCode: code(503)}, "the receiver answered 503"},
		{"no answer", governance.DeliveryAttempt{N: 3, At: at, Error: new("context deadline exceeded")}, "context deadline exceeded"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := governance.WebhookDisabledEvent(someEvent, w, "shop", c.last, at).Envelope()
			if got["type"] != "webhook.disabled" || got["audit_entry_id"] != nil || got["environment"] != nil {
				t.Errorf("envelope %v", got)
			}
			if a, _ := got["actor"].(map[string]any); a["kind"] != "system" {
				t.Errorf("actor %v", got["actor"])
			}
			if e, _ := got["entity"].(map[string]any); e["kind"] != "webhook" || e["id"] != someWebhook.String() || e["key"] != "SIEM" {
				t.Errorf("entity %v", got["entity"])
			}
			data, _ := got["data"].(map[string]any)
			if data["subscription_id"] != someWebhook.String() || data["name"] != "SIEM" || data["last_error"] != c.want {
				t.Errorf("data %v", data)
			}
			if len(data) != 3 {
				t.Errorf("data %v carries more than the subscription and the error", data)
			}
		})
	}
}
