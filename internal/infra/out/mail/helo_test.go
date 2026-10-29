package mail

import "testing"

func TestTheClientGreetsWithTheSendersDomain(t *testing.T) {
	for from, want := range map[string]string{
		"flags@example.com":                   "example.com",
		"Flagnetic <flags@example.com>":       "example.com",
		`"ops@team" <flags@mail.example.com>`: "mail.example.com",
		"not an address":                      "localhost",
	} {
		if got := heloName(from); got != want {
			t.Errorf("heloName(%q) = %q, want %q", from, got, want)
		}
	}
}
