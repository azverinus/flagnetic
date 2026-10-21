package access_test

import (
	"os"
	"testing"

	"github.com/azverinus/flagnetic/internal/domain/access"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	"github.com/azverinus/flagnetic/internal/testkit"
)

func TestMain(m *testing.M) { os.Exit(testkit.Main(m)) }

func TestTheAuthSettingsCacheTrustsItsCopyOnlyWhileListening(t *testing.T) {
	pool := testkit.FreshPool(t)
	store, cache := pgaccess.NewStore(pool), pgaccess.NewAuthSettingsCache(pool)
	save := func(minLength int) {
		t.Helper()
		s := access.DefaultAuthSettings
		s.PasswordMinLength = minLength
		if err := store.SaveAuthSettings(t.Context(), s); err != nil {
			t.Fatal(err)
		}
	}
	read := func(step string, want int) {
		t.Helper()
		got, err := cache.AuthSettings(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if got.PasswordMinLength != want {
			t.Errorf("%s: minimum length = %d, want %d", step, got.PasswordMinLength, want)
		}
	}

	read("fresh", 12)
	save(16)
	read("before listening, a change unannounced", 16)

	cache.Listening(true)
	read("listening", 16)
	save(18)
	read("listening, before the notification arrives", 16)
	cache.Received("")
	read("after the notification", 18)

	save(20)
	cache.Listening(false)
	read("once the listener is down", 20)
	save(21)
	read("while the listener is down, a change unannounced", 21)

	cache.Listening(true)
	read("listening again", 21)
	save(22)
	cache.Forget()
	read("after this replica's own change", 22)
}
