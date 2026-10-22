package delivery_test

import (
	"context"
	"crypto/rand"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// keyNotifications listens on the SDK keys' channel on its own connection
// and returns a function answering the payloads delivered within wait for
// the keys in own. The channel is shared by the whole database, and packages
// run their tests against it at the same time, so a payload for a key another
// test changed is not this test's to judge.
func keyNotifications(t *testing.T, pool *pgxpool.Pool, own map[string]bool) func(wait time.Duration) []string {
	t.Helper()
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Release)
	if _, err = conn.Exec(t.Context(), "LISTEN "+pgdelivery.SDKKeysChannel); err != nil {
		t.Fatal(err)
	}
	return func(wait time.Duration) []string {
		var out []string
		for {
			ctx, cancel := context.WithTimeout(t.Context(), wait)
			n, err := conn.Conn().WaitForNotification(ctx)
			cancel()
			if errors.Is(err, context.DeadlineExceeded) {
				return out
			}
			if err != nil {
				t.Fatal(err)
			}
			if own[n.Payload] {
				out = append(out, n.Payload)
			}
		}
	}
}

func storedKey(t *testing.T, store *pgdelivery.Store, p workspace.Project, e workspace.Environment, kind delivery.SDKKeyKind) delivery.SDKKey {
	t.Helper()
	k, _, err := delivery.NewSDKKey(id.New[delivery.SDKKey](ids), delivery.NewSDKKeyInput{
		Project: p.ID, Environment: e.ID, Kind: kind, Name: "api", Now: testkit.Start,
	}, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.CreateSDKKey(t.Context(), k); err != nil {
		t.Fatal(err)
	}
	return k
}

// A replica keeps the keys it authenticated, so every change to whether a
// key is accepted, and every deletion, cascades included, is announced with
// the key's identifier; a creation or a rename is not.
func TestAChangeDecidingWhetherAKeyIsAcceptedNotifiesItsIdentifier(t *testing.T) {
	pool := testkit.Pool(t)
	store := pgdelivery.NewStore(pool)
	p, envs := project(t, "keys")
	own := map[string]bool{}
	notified := keyNotifications(t, pool, own)
	const quiet = 200 * time.Millisecond

	k := storedKey(t, store, p, envs[0], delivery.SDKKeyClient)
	own[k.ID.String()] = true
	renamed, _, err := k.Update(delivery.SDKKeyChange{Name: ptr("storefront"), AllowedOrigins: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateSDKKey(t.Context(), renamed); err != nil {
		t.Fatal(err)
	}
	if got := notified(quiet); len(got) != 0 {
		t.Errorf("creating and renaming notified %v, want nothing", got)
	}

	limited, _, _ := renamed.Update(delivery.SDKKeyChange{AllowedOrigins: []string{"https://shop.example"}})
	rotated, _, _, _ := limited.Rotate(id.New[delivery.SDKKey](ids), time.Hour, testkit.Start, rand.Reader)
	revoked, _ := rotated.Revoke(testkit.Start)
	for name, change := range map[string]delivery.SDKKey{"origins": limited, "expiry": rotated, "revocation": revoked} {
		if err := store.UpdateSDKKey(t.Context(), change); err != nil {
			t.Fatal(err)
		}
		if got := notified(time.Second); !slices.Equal(got, []string{k.ID.String()}) {
			t.Errorf("a change of %s notified %v, want the key's identifier", name, got)
		}
	}

	other := storedKey(t, store, p, envs[0], delivery.SDKKeyServer)
	own[other.ID.String()] = true
	if err := pgworkspace.NewStore(pool).DeleteEnvironment(t.Context(), envs[0].ID); err != nil {
		t.Fatal(err)
	}
	got := notified(time.Second)
	slices.Sort(got)
	want := []string{k.ID.String(), other.ID.String()}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("deleting the environment notified %v, want both its keys %v", got, want)
	}
	if keys, err := store.SDKKeys(t.Context(), envs[0].ID); err != nil || len(keys) != 0 {
		t.Errorf("the environment's keys outlived it: %v, %v", keys, err)
	}
}

func ptr[T any](v T) *T { return &v }
