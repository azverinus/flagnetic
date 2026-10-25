package testkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
)

// The audit log never holds a secret: a token appears by its hint, a
// password not at all. The test kit guards this across the whole suite
// rather than in one test: it records every secret value the tests create or
// receive through it, and after a package's tests, and before a database of
// one test is dropped, reads every audit entry written there and fails if any
// holds a recorded secret. A secret a test obtains elsewhere is recorded with
// RecordSecret.

var recorded struct {
	mu     sync.Mutex
	values map[string]struct{}
}

// minSecretLength is the shortest value recorded: a shorter one, such as a
// password a test sends only to see it refused, is likely to occur in an
// entry by coincidence and is refused by the password policy anyway.
const minSecretLength = 8

// tokenValueShape matches an API token's whole value, a kind prefix and 40
// base62 characters, and not its hint, which the audit log does record.
var tokenValueShape = regexp.MustCompile(`fn_[a-z]{3}_[0-9A-Za-z]{40}`)

// redactedSecret is what an entry, and a response reading one, writes in
// place of a secret, such as a webhook subscription's: a member named
// secret holding it is the redaction, not a value to guard.
const redactedSecret = "[redacted]"

// RecordSecret records v as a secret value no audit entry may hold.
func RecordSecret(v string) {
	if len(v) < minSecretLength || v == redactedSecret {
		return
	}
	recorded.mu.Lock()
	defer recorded.mu.Unlock()
	if recorded.values == nil {
		recorded.values = map[string]struct{}{}
	}
	recorded.values[v] = struct{}{}
}

// RecordSecretsIn records every token value text holds, such as a command's
// output or a response body.
func RecordSecretsIn(text string) {
	for _, v := range tokenValueShape.FindAllString(text, -1) {
		RecordSecret(v)
	}
}

// RecordedSecrets returns the secret values recorded so far.
func RecordedSecrets() []string {
	recorded.mu.Lock()
	defer recorded.mu.Unlock()
	out := make([]string, 0, len(recorded.values))
	for v := range recorded.values {
		out = append(out, v)
	}
	return out
}

// FindSecretsInAudit reads every audit entry on pool through the audit log's
// own query and returns an error naming each entry holding one of secrets,
// without repeating the secret. A database whose migrations have not created
// the audit log yet holds no entry.
func FindSecretsInAudit(ctx context.Context, pool *pgxpool.Pool, secrets []string) error {
	if len(secrets) == 0 {
		return nil
	}
	log := pggovernance.NewAuditLog(pool)
	var (
		leaks []string
		after governance.EntryID
	)
	for {
		page, err := log.Entries(ctx, appgovernance.AuditFilter{}, after, 500)
		if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading the audit log: %w", err)
		}
		for _, e := range page {
			if field := fieldHolding(e, secrets); field != "" {
				leaks = append(leaks, fmt.Sprintf("entry %s (%s) holds a secret value in %s", e.ID, e.Action, field))
			}
		}
		if len(page) < 500 {
			break
		}
		after = page[len(page)-1].ID
	}
	if len(leaks) > 0 {
		return errors.New("a secret value reached the audit log:\n\t" + strings.Join(leaks, "\n\t"))
	}
	return nil
}

// fieldHolding returns the name of the first field of e holding one of
// secrets, or "" when none does. Before and after are searched as the JSON
// they are, for the secret itself and in its escaped form.
func fieldHolding(e governance.Entry, secrets []string) string {
	fields := []struct{ name, value string }{
		{"actor id", e.Actor.ID}, {"actor name", e.Actor.Name}, {"actor member", e.Actor.MemberID},
		{"action", e.Action}, {"entity kind", e.Entity.Kind}, {"entity id", e.Entity.ID}, {"entity key", e.Entity.Key},
		{"request", e.RequestID}, {"before", rawJSON(e.Before)}, {"after", rawJSON(e.After)},
	}
	for _, f := range fields {
		for _, s := range secrets {
			if strings.Contains(f.value, s) || strings.Contains(f.value, escaped(s)) {
				return f.name
			}
		}
	}
	return ""
}

func rawJSON(v any) string {
	if raw, ok := v.(json.RawMessage); ok {
		return string(raw)
	}
	return ""
}

// escaped returns s as it reads inside a JSON string.
func escaped(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

// auditHoldsNoRecordedSecret is the guard run on the package's database
// after its tests.
func auditHoldsNoRecordedSecret(ctx context.Context, pool *pgxpool.Pool) error {
	return FindSecretsInAudit(ctx, pool, RecordedSecrets())
}

// guardDatabase runs the guard on the database of one test at url when the
// test ends, before the database is dropped.
func guardDatabase(t testing.TB, url string) {
	t.Cleanup(func() {
		secrets := RecordedSecrets()
		if len(secrets) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		pool, err := pgxpool.New(ctx, url)
		if err != nil {
			t.Error(err)
			return
		}
		defer pool.Close()
		if err := FindSecretsInAudit(ctx, pool, secrets); err != nil {
			t.Error(err)
		}
	})
}

// recordingTransport records the secrets of every request a test client
// sends and every response it receives: token values by their shape, the
// string members of a JSON body named as a password or a secret, and the
// values of the cookies a response sets.
type recordingTransport struct{}

func (recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return nil, err
		}
		recordBody(body)
		r.Body = io.NopCloser(bytes.NewReader(body))
	}
	res, err := http.DefaultTransport.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil {
		return nil, err
	}
	recordBody(body)
	res.Body = io.NopCloser(bytes.NewReader(body))
	for _, c := range res.Cookies() {
		RecordSecret(c.Value)
	}
	return res, nil
}

func recordBody(body []byte) {
	RecordSecretsIn(string(body))
	var v any
	if json.Unmarshal(body, &v) == nil {
		recordSecretMembers(v)
	}
}

// recordSecretMembers records the string value of every member of v, at any
// depth, whose name says it holds a password or a secret.
func recordSecretMembers(v any) {
	switch v := v.(type) {
	case map[string]any:
		for k, m := range v {
			name := strings.ToLower(k)
			if s, ok := m.(string); ok && (strings.Contains(name, "password") || strings.Contains(name, "secret")) {
				RecordSecret(s)
			}
			recordSecretMembers(m)
		}
	case []any:
		for _, m := range v {
			recordSecretMembers(m)
		}
	}
}
