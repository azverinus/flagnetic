package sdk

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	"github.com/azverinus/flagnetic/internal/domain/delivery"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

// eventMetrics count the events the replica ingests.
type eventMetrics struct {
	ingested, dropped *prometheus.CounterVec
}

func newEventMetrics(reg prometheus.Registerer) eventMetrics {
	m := eventMetrics{
		ingested: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: "events", Name: "ingested_total",
			Help: "Events SDKs posted that were stored, by kind.",
		}, []string{"kind"}),
		dropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metrics.Namespace, Subsystem: "events", Name: "dropped_total",
			Help: "Events SDKs posted that were not stored, by reason: a rejection code, duplicate, " +
				"or unknown_flag for a summary row naming a flag the project does not have.",
		}, []string{"reason"}),
	}
	if reg != nil {
		reg.MustRegister(m.ingested, m.dropped)
	}
	return m
}

func (m eventMetrics) observe(res appdelivery.BatchResult) {
	for kind, n := range res.Accepted {
		m.ingested.WithLabelValues(string(kind)).Add(float64(n))
	}
	if res.Duplicates > 0 {
		m.dropped.WithLabelValues("duplicate").Add(float64(res.Duplicates))
	}
	if res.DroppedRows > 0 {
		m.dropped.WithLabelValues("unknown_flag").Add(float64(res.DroppedRows))
	}
	for _, r := range res.Rejected {
		m.dropped.WithLabelValues(string(r.Code)).Inc()
	}
}

// envelope is an events batch as posted, its members kept raw so each is
// checked on its own and every fault named.
type envelope struct {
	SchemaVersion json.RawMessage `json:"schema_version"`
	SDK           json.RawMessage `json:"sdk"`
	SentAt        json.RawMessage `json:"sent_at"`
	Events        json.RawMessage `json:"events"`
}

// errNotABatch answers an envelope that breaks its shape; its violations
// name every member at fault.
const errNotABatch = "the body is not a batch of events; errors names every member at fault"

// decodeBatch reads an events batch from body: refused as a whole for a body
// that is not one JSON object, an envelope that breaks its shape, or more
// events than a batch holds. The events themselves are judged one by one
// later, so one malformed event never costs the others.
func decodeBatch(body io.Reader) (appdelivery.EventBatch, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return appdelivery.EventBatch{}, err
	}
	var env envelope
	dec := json.NewDecoder(bytes.NewReader(data))
	if err = dec.Decode(&env); err != nil {
		return appdelivery.EventBatch{}, flaghttp.InvalidRequest("the body is not a batch of events: " + err.Error())
	}
	if _, err = dec.Token(); !errors.Is(err, io.EOF) {
		return appdelivery.EventBatch{}, flaghttp.InvalidRequest("the body holds more than one JSON value")
	}
	var (
		b          appdelivery.EventBatch
		violations []apperrors.Violation
	)
	add := func(pointer, code, detail string, params map[string]any) {
		violations = append(violations, apperrors.Violation{Pointer: pointer, Code: code, Detail: detail, Params: params})
	}
	if isNull(env.Events) {
		add("/events", "required", "events is missing", nil)
	} else if err := json.Unmarshal(env.Events, &b.Events); err != nil {
		add("/events", "invalid_type", "events must be a list of events", nil)
	} else if len(b.Events) > delivery.MaxBatchEvents {
		return appdelivery.EventBatch{}, flaghttp.RequestTooLarge("a batch holds at most " +
			strconv.Itoa(delivery.MaxBatchEvents) + " events, this one " + strconv.Itoa(len(b.Events)))
	}
	var version int
	switch {
	case isNull(env.SchemaVersion):
		add("/schema_version", "required", "schema_version is missing", nil)
	case json.Unmarshal(env.SchemaVersion, &version) != nil:
		add("/schema_version", "invalid_type", "schema_version must be an integer", nil)
	case version != 1:
		add("/schema_version", "out_of_range", "schema_version must be 1", map[string]any{"min": 1, "max": 1})
	}
	var sdk map[string]json.RawMessage
	switch {
	case isNull(env.SDK):
		add("/sdk", "required", "sdk is missing", nil)
	case json.Unmarshal(env.SDK, &sdk) != nil:
		add("/sdk", "invalid_type", "sdk must be an object naming the SDK", nil)
	default:
		b.SDK.Name = sdkString(sdk, "name", true, add)
		b.SDK.Version = sdkString(sdk, "version", true, add)
		b.SDK.Platform = sdkString(sdk, "platform", false, add)
	}
	var sentAt string
	switch {
	case isNull(env.SentAt):
		add("/sent_at", "required", "sent_at is missing", nil)
	case json.Unmarshal(env.SentAt, &sentAt) != nil:
		add("/sent_at", "invalid_type", "sent_at must be an RFC 3339 time", nil)
	default:
		if _, err := time.Parse(time.RFC3339Nano, sentAt); err != nil {
			add("/sent_at", "invalid_type", "sent_at must be an RFC 3339 time", nil)
		}
	}
	if len(violations) > 0 {
		return appdelivery.EventBatch{}, flaghttp.MalformedRequest(errNotABatch, violations...)
	}
	return b, nil
}

// isNull reports whether a member is absent or null.
func isNull(raw json.RawMessage) bool { return len(raw) == 0 || string(raw) == "null" }

// sdkString reads the member name of the envelope's sdk: a string of at most
// delivery.MaxEventString bytes without U+0000, non-empty when required,
// absent or null otherwise.
func sdkString(sdk map[string]json.RawMessage, name string, required bool,
	add func(pointer, code, detail string, params map[string]any),
) string {
	pointer := "/sdk/" + name
	raw := sdk[name]
	if isNull(raw) {
		if required {
			add(pointer, "required", "sdk."+name+" is missing", nil)
		}
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		add(pointer, "invalid_type", "sdk."+name+" must be a string", nil)
		return ""
	}
	switch {
	case required && s == "":
		add(pointer, "required", "sdk."+name+" must not be empty", nil)
	case len(s) > delivery.MaxEventString:
		add(pointer, "too_long", "sdk."+name+" is over "+strconv.Itoa(delivery.MaxEventString)+" bytes",
			map[string]any{"max": delivery.MaxEventString})
	case strings.ContainsRune(s, 0):
		add(pointer, "invalid_character", "sdk."+name+" must not contain U+0000", nil)
	}
	return s
}

// errNotABatchMedia answers an events batch sent as another media type than
// JSON or plain text.
var errNotABatchMedia = flaghttp.UnsupportedMediaType("the body must be sent as application/json or text/plain")

// requireBatchMedia refuses an events batch whose body is declared neither
// JSON nor plain text. A closing page hands its last batch to
// navigator.sendBeacon, which sends a string as text/plain and so needs no
// CORS preflight, which browsers do not send for such a request; the body
// is read as JSON either way. A parameter such as charset is accepted.
func requireBatchMedia(r *http.Request) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (media != "application/json" && media != "text/plain") {
		return errNotABatchMedia
	}
	return nil
}

// PostEvents judges a batch of events and stores those accepted, answering
// 202 once they are durable.
func (a *api) PostEvents(w http.ResponseWriter, r *http.Request, params PostEventsParams) {
	if err := checkSchemaVersion(params.FlagneticSchemaVersion); err != nil {
		a.writeError(w, r, err)
		return
	}
	if err := requireBatchMedia(r); err != nil {
		a.writeError(w, r, err)
		return
	}
	b, err := decodeBatch(r.Body)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	res, err := a.h.Ingest.Handle(r.Context(), keyOf(r.Context()), b)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	a.events.observe(res)
	a.h.Inventory.Note(keyOf(r.Context()), delivery.IdentifySDK(r.UserAgent(), b.SDK), "", 0)
	out := EventBatchResult{
		Accepted: res.AcceptedTotal(), Duplicates: res.Duplicates, DroppedRows: res.DroppedRows,
		Rejected: make([]EventRejection, 0, len(res.Rejected)),
	}
	for _, rj := range res.Rejected {
		e := EventRejection{Index: rj.Index, Code: string(rj.Code), Detail: rj.Detail}
		if rj.EventID != "" {
			e.EventId = &rj.EventID
		}
		out.Rejected = append(out.Rejected, e)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(out)
}
