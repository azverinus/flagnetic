package flagnetic

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Limits of POST /sdk/v1/events of the SDK API. The body limit is on the
// decompressed envelope, so the client packs batches against it before
// gzipping.
const (
	maxEventsPerBatch = 500
	maxSummaryRows    = 1000
	maxBatchBytes     = 1 << 20
	// maxIngestResultBytes bounds the 202 answer the client reads.
	maxIngestResultBytes = 1 << 20
)

// sdkName is this SDK's name in the envelope and in the SDK inventory.
const sdkName = "flagnetic-go"

// event is one event of an ingest batch. Evaluation summaries implement it
// today; exposures and aliases reuse the envelope and the retry policy
// through the same interface.
type event interface {
	// id is the event's event_id, which the platform deduplicates on, so
	// every retry of a batch carries the same one.
	id() string
}

// eventBatch is the envelope of a POST /sdk/v1/events body.
type eventBatch struct {
	SchemaVersion int         `json:"schema_version"`
	SDK           sdkIdentity `json:"sdk"`
	SentAt        time.Time   `json:"sent_at"`
	Events        []event     `json:"events"`
}

// sdkIdentity names the SDK that sent a batch; the SDK inventory is built
// from it.
type sdkIdentity struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
}

// rejection is one entry of a 202 answer's rejected list: an event the
// platform refused, which is never sent again.
type rejection struct {
	Index   int    `json:"index"`
	EventID string `json:"event_id"`
	Code    string `json:"code"`
	Detail  string `json:"detail"`
}

// ingestResult is the 202 answer of the events endpoint.
type ingestResult struct {
	Accepted    int         `json:"accepted"`
	Duplicates  int         `json:"duplicates"`
	DroppedRows int         `json:"dropped_rows"`
	Rejected    []rejection `json:"rejected"`
}

// sendOutcome says what the platform did with a batch and whether the
// batch may be sent again.
type sendOutcome int

const (
	// sendAccepted: 202. The batch is done, rejected events included:
	// a rejection is final.
	sendAccepted sendOutcome = iota
	// sendRetry: 429, 5xx or a network error. The same batch, with the
	// same event IDs, may be sent again after a backoff.
	sendRetry
	// sendUnauthorized: 401 or 403. Retriable too, but at the
	// unauthorized cap, since an operator restoring the key is what ends
	// it.
	sendUnauthorized
	// sendDropped: any other 4xx. Sending it again would fail the same
	// way, so the batch goes and the cause is logged.
	sendDropped
)

// sendResult is the outcome of one attempt at one batch.
type sendResult struct {
	outcome sendOutcome
	result  ingestResult
	// retryAfter is the Retry-After the platform asked for, zero when it
	// asked for none.
	retryAfter time.Duration
	status     int
	err        error
}

// ingest posts event batches to POST /sdk/v1/events: it builds the
// envelope, gzips it, and reads the 202 answer. It holds no queue and no
// timers; what to send and when to send it again is the caller's.
type ingest struct {
	url        string
	sdkKey     string
	http       *http.Client
	timeout    time.Duration
	identity   sdkIdentity
	now        func() time.Time
	keyRevoked func(status int)
}

func newIngest(c *Client, now func() time.Time) *ingest {
	return &ingest{
		url:        c.eventsURL,
		sdkKey:     c.sdkKey,
		http:       c.http,
		timeout:    c.wait.requestTimeout,
		identity:   sdkIdentity{Name: sdkName, Version: Version, Platform: runtime.GOOS},
		now:        now,
		keyRevoked: c.keyRevoked,
	}
}

// post sends one batch and reports what the platform answered. It never
// retries: the caller decides that from the outcome, sending the same
// events again so their IDs stay the same.
func (i *ingest) post(ctx context.Context, events []event) sendResult {
	body, err := i.encode(events)
	if err != nil {
		// Unreachable for the events this SDK builds; a batch that cannot
		// be encoded would fail the same way every time.
		return sendResult{outcome: sendDropped, err: err}
	}
	rctx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, i.url, bytes.NewReader(body))
	if err != nil {
		return sendResult{outcome: sendDropped, err: err}
	}
	req.Header.Set("Authorization", "Bearer "+i.sdkKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Flagnetic-Schema-Version", strconv.Itoa(schemaVersionMax))
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := i.http.Do(req)
	if err != nil {
		return sendResult{outcome: sendRetry, err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == http.StatusAccepted:
		return sendResult{outcome: sendAccepted, status: resp.StatusCode, result: i.read(resp)}
	case resp.StatusCode == http.StatusTooManyRequests:
		return sendResult{outcome: sendRetry, status: resp.StatusCode,
			retryAfter: retryAfter(resp.Header.Get("Retry-After")), err: errors.New(resp.Status)}
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		i.keyRevoked(resp.StatusCode)
		return sendResult{outcome: sendUnauthorized, status: resp.StatusCode, err: errors.New(resp.Status)}
	case resp.StatusCode >= 500:
		return sendResult{outcome: sendRetry, status: resp.StatusCode, err: errors.New(resp.Status)}
	default:
		return sendResult{outcome: sendDropped, status: resp.StatusCode, err: errors.New(resp.Status)}
	}
}

// encode builds the envelope and gzips it. The batch limits are the
// caller's: it packs events so that the encoded envelope fits.
func (i *ingest) encode(events []event) ([]byte, error) {
	doc, err := json.Marshal(eventBatch{
		SchemaVersion: schemaVersionMax,
		SDK:           i.identity,
		SentAt:        i.now().UTC(),
		Events:        events,
	})
	if err != nil {
		return nil, fmt.Errorf("flagnetic: encode an ingest batch: %w", err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(doc); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// read decodes the 202 answer. A body that does not decode leaves the
// counts zero: the batch was accepted either way, so nothing is retried.
func (i *ingest) read(resp *http.Response) ingestResult {
	var r io.Reader
	r = io.LimitReader(resp.Body, maxIngestResultBytes)
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(r)
		if err != nil {
			return ingestResult{}
		}
		defer func() { _ = zr.Close() }()
		r = zr
	}
	doc, err := io.ReadAll(r)
	if err != nil {
		return ingestResult{}
	}
	var out ingestResult
	_ = json.Unmarshal(doc, &out)
	return out
}

// retryAfter reads a Retry-After header in its delta-seconds form; an HTTP
// date or anything else means the SDK backs off on its own.
func retryAfter(header string) time.Duration {
	if header == "" {
		return 0
	}
	seconds, err := strconv.Atoi(strings.TrimSpace(header))
	if err != nil || seconds < 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// envelopeOverhead is how many bytes an empty envelope costs, so packing
// events into a batch can stay under the endpoint's body limit.
func (i *ingest) envelopeOverhead() int {
	doc, err := json.Marshal(eventBatch{
		SchemaVersion: schemaVersionMax,
		SDK:           i.identity,
		SentAt:        i.now().UTC(),
		Events:        []event{},
	})
	if err != nil {
		return 512
	}
	return len(doc)
}

// pack splits events into batches that the endpoint accepts: at most 500
// events and, decompressed, under 1 MiB. An event too large on its own
// still becomes a batch of one, which the platform answers 413 and the
// caller drops.
func (i *ingest) pack(events []event) [][]event {
	budget := maxBatchBytes - i.envelopeOverhead()
	var batches [][]event
	var current []event
	size := 0
	for _, e := range events {
		encoded, err := json.Marshal(e)
		if err != nil {
			encoded = nil
		}
		// One comma per event after the first.
		cost := len(encoded) + 1
		if len(current) > 0 && (len(current) == maxEventsPerBatch || size+cost > budget) {
			batches = append(batches, current)
			current, size = nil, 0
		}
		current = append(current, e)
		size += cost
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

// logIngestFailed reports one attempt at a batch that will be made again,
// with the same event IDs, after wait. Every caller of the events endpoint
// logs it the same way, naming its own kind of batch.
func logIngestFailed(log func(slog.Level, Event, map[string]any), kind string, group []event, res sendResult, wait time.Duration) {
	fields := map[string]any{"kind": kind, "events": len(group), "retry_in": wait.String()}
	addFailure(fields, res)
	log(slog.LevelWarn, EventIngestFailed, fields)
}

// logBatchDropped reports a batch given up on: a 4xx other than 429 means
// sending it again would fail the same way.
func logBatchDropped(log func(slog.Level, Event, map[string]any), kind string, group []event, res sendResult) {
	fields := map[string]any{"kind": kind, "cause": "client_error", "events": eventIDs(group)}
	addFailure(fields, res)
	log(slog.LevelWarn, EventQueueDropped, fields)
}

// addFailure adds what the platform said, where it said anything.
func addFailure(fields map[string]any, res sendResult) {
	if res.status != 0 {
		fields["status"] = res.status
	}
	if res.err != nil {
		fields["error"] = res.err.Error()
	}
}
