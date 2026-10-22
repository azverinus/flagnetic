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
// and returns a function answering the payloads delivered within wait that
// name one of the given keys. A notification channel spans the whole
// database while each test package works in a schema of its own, so keys
// that tests of other packages change at the same time are announced on
// this connection too; only the keys this test stored say anything about it.
func keyNotifications(t *testing.T, pool *pgxpool.Pool) func(wait time.Duration, keys ...delivery.SDKKey) []string {
	t.Helper()
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Release)
	if _, err = conn.Exec(t.Context(), "LISTEN "+pgdelivery.SDKKeysChannel); err != nil {
		t.Fatal(err)
	}
	return func(wait time.Duration, keys ...delivery.SDKKey) []string {
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
			if slices.ContainsFunc(keys, func(k delivery.SDKKey) bool { return k.ID.String() == n.Payload }) {
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
	notified := keyNotifications(t, pool)
	const quiet = 200 * time.Millisecond

	k := storedKey(t, store, p, envs[0], delivery.SDKKeyClient)
	renamed, _, err := k.Update(delivery.SDKKeyChange{Name: ptr("storefront"), AllowedOrigins: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateSDKKey(t.Context(), renamed); err != nil {
		t.Fatal(err)
	}
	if got := notified(quiet, k); len(got) != 0 {
		t.Errorf("creating and renaming notified %v, want nothing", got)
	}

	limited, _, _ := renamed.Update(delivery.SDKKeyChange{AllowedOrigins: []string{"https://shop.example"}})
	rotated, _, _, _ := limited.Rotate(id.New[delivery.SDKKey](ids), time.Hour, testkit.Start, rand.Reader)
	revoked, _ := rotated.Revoke(testkit.Start)
	for name, change := range map[string]delivery.SDKKey{"origins": limited, "expiry": rotated, "revocation": revoked} {
		if err := store.UpdateSDKKey(t.Context(), change); err != nil {
			t.Fatal(err)
		}
		if got := notified(time.Second, k); !slices.Equal(got, []string{k.ID.String()}) {
			t.Errorf("a change of %s notified %v, want the key's identifier", name, got)
		}
	}

	other := storedKey(t, store, p, envs[0], delivery.SDKKeyServer)
	if err := pgworkspace.NewStore(pool).DeleteEnvironment(t.Context(), envs[0].ID); err != nil {
		t.Fatal(err)
	}
	got := notified(time.Second, k, other)
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
