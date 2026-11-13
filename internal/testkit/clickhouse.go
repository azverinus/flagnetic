package testkit

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// clickHouseImage is the ClickHouse every sink test meets when CLICKHOUSE_URL
// is not set: an LTS release, pinned so a test run never drifts with "latest".
const clickHouseImage = "clickhouse/clickhouse-server:25.8"

// ClickHouse is the package's ClickHouse server.
type ClickHouse struct {
	// URL is its HTTP interface as a sink's configuration names it, without
	// credentials.
	URL string
	// Username and Password are the user a sink authenticates as.
	Username, Password string
}

var clickHouse struct {
	mu      sync.Mutex
	server  *ClickHouse
	stopper func()
}

// stopClickHouse ends the container the package started.
func stopClickHouse() {
	clickHouse.mu.Lock()
	defer clickHouse.mu.Unlock()
	if clickHouse.stopper != nil {
		clickHouse.stopper()
	}
	clickHouse.server, clickHouse.stopper = nil, nil
}

// StartClickHouse returns the package's ClickHouse, starting it on first use:
// the server CLICKHOUSE_URL names (http://user:password@host:8123) when it is
// set, otherwise a container stopped after the package's last test. Tests share
// it and isolate by database, see NewDatabase.
func StartClickHouse(t testing.TB) *ClickHouse {
	t.Helper()
	clickHouse.mu.Lock()
	defer clickHouse.mu.Unlock()
	if clickHouse.server != nil {
		return clickHouse.server
	}
	ch := &ClickHouse{}
	if raw := os.Getenv("CLICKHOUSE_URL"); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.User == nil {
			t.Fatalf("CLICKHOUSE_URL is not http://user:password@host:port")
		}
		ch.Username = u.User.Username()
		ch.Password, _ = u.User.Password()
		u.User = nil
		ch.URL = strings.TrimSuffix(u.String(), "/")
	} else {
		ch.Username, ch.Password = "flagnetic", "clickhouse-test-"+strings.ToLower(rand.Text())[:12]
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		c, err := testcontainers.Run(ctx, clickHouseImage,
			testcontainers.WithExposedPorts("8123/tcp"),
			testcontainers.WithEnv(map[string]string{
				"CLICKHOUSE_USER":                      ch.Username,
				"CLICKHOUSE_PASSWORD":                  ch.Password,
				"CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1",
			}),
			testcontainers.WithWaitStrategy(wait.ForHTTP("/ping").WithPort("8123/tcp").WithStartupTimeout(2*time.Minute)),
		)
		if c != nil {
			clickHouse.stopper = func() { _ = testcontainers.TerminateContainer(c) }
		}
		if err != nil {
			t.Fatalf("starting ClickHouse (is Docker running? or set CLICKHOUSE_URL): %v", err)
		}
		endpoint, err := c.PortEndpoint(ctx, "8123/tcp", "")
		if err != nil {
			t.Fatal(err)
		}
		ch.URL = "http://" + endpoint
	}
	clickHouse.server = ch
	return ch
}

// NewDatabase creates a uniquely named database for one test, so tests sharing
// the server never see each other's tables.
func (ch *ClickHouse) NewDatabase(t testing.TB) string {
	t.Helper()
	name := "test_" + strings.ToLower(rand.Text())[:16]
	ch.Exec(t, "CREATE DATABASE "+name)
	return name
}

// Exec runs statement, failing the test on any error.
func (ch *ClickHouse) Exec(t testing.TB, statement string) {
	t.Helper()
	if _, err := ch.do(statement); err != nil {
		t.Fatalf("ClickHouse %q: %v", statement, err)
	}
}

// Query runs query and returns its rows, every number a json.Number.
func (ch *ClickHouse) Query(t testing.TB, query string) []map[string]any {
	t.Helper()
	body, err := ch.do(query + " FORMAT JSONEachRow")
	if err != nil {
		t.Fatalf("ClickHouse %q: %v", query, err)
	}
	var rows []map[string]any
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		d := json.NewDecoder(strings.NewReader(sc.Text()))
		d.UseNumber()
		var row map[string]any
		if err := d.Decode(&row); err != nil {
			t.Fatalf("decode ClickHouse row %q: %v", sc.Text(), err)
		}
		rows = append(rows, row)
	}
	return rows
}

func (ch *ClickHouse) do(statement string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ch.URL+"/", strings.NewReader(statement))
	if err != nil {
		return "", err
	}
	req.Header.Set("X-ClickHouse-User", ch.Username)
	req.Header.Set("X-ClickHouse-Key", ch.Password)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: %s", res.Status, b)
	}
	return string(b), nil
}
