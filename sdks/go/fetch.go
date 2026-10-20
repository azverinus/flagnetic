package flagnetic

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/azverinus/flagnetic/eval"
)

// Defaults for server mode.
const (
	defaultRequestTimeout  = 10 * time.Second
	defaultPollInterval    = 5 * time.Minute
	minPollInterval        = 30 * time.Second
	defaultRetryBase       = time.Second
	defaultRetryCap        = 5 * time.Minute
	defaultUnauthorizedCap = 15 * time.Minute
	// maxRulesetBytes bounds a decompressed ruleset document.
	maxRulesetBytes = 256 << 20
)

// Causes of a fetch_failed event.
const (
	causeTimeout           = "timeout"
	causeNetwork           = "network"
	causeHTTPStatus        = "http_status"
	causeUnauthorized      = "unauthorized"
	causeParse             = "parse"
	causeSchemaUnsupported = "schema_unsupported"
	causeWrongScope        = "wrong_scope"
	causeVersionDowngrade  = "version_downgrade"
)

// clientKeyPrefix marks a client SDK key, which the ruleset endpoint refuses.
const clientKeyPrefix = "fn_cli_"

// fetchOutcome is how a fetch ended, deciding the next wait.
type fetchOutcome int

const (
	fetchSucceeded fetchOutcome = iota
	fetchFailed
	fetchUnauthorized
	fetchCanceled
)

func userAgent() string {
	return fmt.Sprintf("flagnetic-go/%s (%s)", Version, runtime.GOOS)
}

// run is the background loop of an online client: fetch, then wait until
// the next reason to fetch — the poll interval after a success, a backoff
// after a failure, a catch-up delay while a replica is behind, or a change
// the stream announces — until ctx is done.
func (c *Client) run(ctx context.Context) {
	// The change stream opens before the first fetch, so that no change
	// falls between the two. A connection that neither comes up nor fails
	// — a black hole between the SDK and the instance — must not hold the
	// first fetch for longer than one request would take.
	opening := time.NewTimer(c.wait.requestTimeout)
	select {
	case <-c.streamReady:
	case <-opening.C:
	case <-ctx.Done():
		opening.Stop()
		return
	}
	opening.Stop()
	// failures counts consecutive failures of any kind; the kind of the
	// latest only picks the cap.
	failures := 0
	// announced is the version the change stream last asked for, and
	// catchUps how many times this fetch loop has re-asked for it.
	var announced int64
	catchUps := 0
	for {
		// This fetch answers whatever the stream has announced so far.
		select {
		case <-c.wake:
		default:
		}
		outcome := c.fetch(ctx)
		if outcome == fetchCanceled || ctx.Err() != nil {
			return
		}
		c.markReady()
		var wait time.Duration
		switch outcome {
		case fetchFailed:
			failures++
			wait = c.jitter(backoffCeiling(failures, c.wait.retryBase, c.wait.retryCap))
		case fetchUnauthorized:
			failures++
			wait = c.jitter(backoffCeiling(failures, c.wait.retryBase, c.wait.unauthorizedCap))
		default:
			failures = 0
			if target := c.target.Load(); target != announced {
				announced, catchUps = target, 0
			}
			if c.heldVersion() < announced && catchUps < len(c.wait.catchUp) {
				// The replica that answered is still behind the version
				// the event carried: ask again before the safety poll.
				wait = c.wait.catchUp[catchUps]
				catchUps++
			} else {
				wait = c.pollWait()
			}
		}
		if !c.waitNext(ctx, wait) {
			return
		}
	}
}

// pollWait is how long the safety poll waits: the poll interval while the
// change stream is connected, the short interval while it is down or
// disabled.
func (c *Client) pollWait() time.Duration {
	if c.streamConnected.Load() {
		return c.wait.poll
	}
	return c.wait.downPoll
}

// waitNext waits d, returning early when the change stream asks for a
// fetch, and false when the client is closing.
func (c *Client) waitNext(ctx context.Context, d time.Duration) bool {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watching := make(chan struct{})
	go func() {
		defer close(watching)
		select {
		case <-c.wake:
			cancel()
		case <-wctx.Done():
		}
	}()
	c.sleep(wctx, d)
	cancel()
	<-watching
	return ctx.Err() == nil
}

// backoffCeiling is the exponential ceiling of the nth consecutive
// failure: base doubling up to limit.
func backoffCeiling(n int, base, limit time.Duration) time.Duration {
	d := base
	for i := 1; i < n && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

// fullJitter draws a wait uniformly from [0, ceiling].
func fullJitter(ceiling time.Duration) time.Duration {
	return rand.N(ceiling + 1) // #nosec G404 -- spreading retries needs no cryptographic randomness
}

// sleep waits d, returning false when ctx is done first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// fetch asks for the ruleset once and applies the result to the fallback
// chain.
func (c *Client) fetch(ctx context.Context) fetchOutcome {
	held := c.ruleset.Load()
	rctx, cancel := context.WithTimeout(ctx, c.wait.requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, c.rulesetURL, nil)
	if err != nil {
		return c.failed(ctx, causeNetwork, 0, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.sdkKey)
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Flagnetic-Schema-Version", strconv.Itoa(schemaVersionMax))
	req.Header.Set("Accept-Encoding", "gzip")
	if held != nil {
		req.Header.Set("If-None-Match", strconv.Quote(strconv.FormatInt(held.rs.Version, 10)))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return c.failed(ctx, transportCause(err), 0, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotModified:
		if held == nil {
			return c.failed(ctx, causeHTTPStatus, resp.StatusCode, errors.New("304 with no ruleset held"))
		}
		c.succeeded()
		return fetchSucceeded
	case http.StatusUnauthorized, http.StatusForbidden:
		c.keyRevoked(resp.StatusCode)
		if c.failed(ctx, causeUnauthorized, resp.StatusCode, errors.New(resp.Status)) == fetchCanceled {
			return fetchCanceled
		}
		return fetchUnauthorized
	default:
		return c.failed(ctx, causeHTTPStatus, resp.StatusCode, errors.New(resp.Status))
	}

	doc, err := readBody(resp)
	if err != nil {
		return c.failed(ctx, transportCause(err), 0, err)
	}
	rs, cause, err := decodeServerRuleset(doc)
	if err != nil {
		if cause == causeSchemaUnsupported && !c.schemaLogged.Swap(true) {
			c.log(slog.LevelError, EventSchemaUnsupported, map[string]any{"schema_version": schemaVersionOf(doc), "supported": supportedSchemaVersions()})
		}
		return c.failed(ctx, cause, 0, err)
	}
	if held != nil && rs.Version < held.rs.Version {
		return c.failed(ctx, causeVersionDowngrade, 0, fmt.Errorf("version %d is below the held %d", rs.Version, held.rs.Version))
	}
	changed := c.swap(rs, "server", time.Now())
	c.persist(doc)
	c.succeeded()
	c.fireChange(changed)
	return fetchSucceeded
}

// succeeded marks the most recent attempt a success: fresh, not stale, and
// a later 401 or 403 is news again.
func (c *Client) succeeded() {
	c.revoked.Store(false)
	c.staleSince.Store(0)
	c.setStage(StageFresh)
}

// failed logs fetch_failed, marks the ruleset stale unless it already is,
// and moves fresh to last_good; last_good and built_in stay. A fetch cut short by Close is not a failure.
func (c *Client) failed(ctx context.Context, cause string, status int, err error) fetchOutcome {
	if ctx.Err() != nil {
		return fetchCanceled
	}
	fields := map[string]any{"cause": cause, "error": err.Error()}
	if status != 0 {
		fields["status"] = status
	}
	c.log(slog.LevelWarn, EventFetchFailed, fields)
	c.staleSince.CompareAndSwap(0, time.Now().UnixNano())
	if c.Stage() == StageFresh {
		c.setStage(StageLastGood)
	}
	return fetchFailed
}

// transportCause tells a timeout from any other transport error.
func transportCause(err error) string {
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return causeTimeout
	}
	return causeNetwork
}

// readBody reads the response body, gunzipping it when the server did,
// up to maxRulesetBytes.
func readBody(resp *http.Response) ([]byte, error) {
	var r io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer func() { _ = zr.Close() }()
		r = zr
	}
	doc, err := io.ReadAll(io.LimitReader(r, maxRulesetBytes+1))
	if err == nil && len(doc) > maxRulesetBytes {
		return nil, fmt.Errorf("ruleset larger than %d bytes", maxRulesetBytes)
	}
	return doc, err
}

// decodeServerRuleset accepts a ruleset document only when it parses,
// carries a supported schema_version and is of server scope; otherwise it
// returns the fetch_failed cause.
func decodeServerRuleset(doc []byte) (*eval.Ruleset, string, error) {
	var head struct {
		SchemaVersion *int   `json:"schema_version"`
		Scope         string `json:"scope"`
	}
	if err := json.Unmarshal(doc, &head); err != nil {
		return nil, causeParse, fmt.Errorf("ruleset: %w", err)
	}
	if head.SchemaVersion == nil {
		return nil, causeParse, errors.New("ruleset: no schema_version")
	}
	if *head.SchemaVersion < schemaVersionMin || *head.SchemaVersion > schemaVersionMax {
		return nil, causeSchemaUnsupported, fmt.Errorf("ruleset: schema_version %d, this SDK reads %d–%d", *head.SchemaVersion, schemaVersionMin, schemaVersionMax)
	}
	if head.Scope != eval.ScopeServer {
		return nil, causeWrongScope, fmt.Errorf("ruleset: scope %q, want %q", head.Scope, eval.ScopeServer)
	}
	rs, err := eval.Decode(doc)
	if err != nil {
		return nil, causeParse, err
	}
	return rs, "", nil
}

// supportedSchemaVersions lists every schema_version from schemaVersionMin
// to schemaVersionMax, as the schema_unsupported event reports them.
func supportedSchemaVersions() []int {
	var vs []int
	for v := schemaVersionMin; v <= schemaVersionMax; v++ {
		vs = append(vs, v)
	}
	return vs
}

func schemaVersionOf(doc []byte) int {
	var head struct {
		SchemaVersion int `json:"schema_version"`
	}
	_ = json.Unmarshal(doc, &head)
	return head.SchemaVersion
}
