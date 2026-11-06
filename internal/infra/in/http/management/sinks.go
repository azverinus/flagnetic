package management

import (
	"context"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// ListSinks answers the project's sinks, never their secrets.
func (a *api) ListSinks(ctx context.Context, _ ListSinksRequestObject) (ListSinksResponseObject, error) {
	list, err := a.h.ListSinks.Handle(ctx, projectID(ctx))
	if err != nil {
		return nil, err
	}
	return ListSinks200JSONResponse{Items: each(list, sinkOut)}, nil
}

// CreateSink creates a sink, disabled, and answers a webhook sink's secret,
// this once.
func (a *api) CreateSink(ctx context.Context, req CreateSinkRequestObject) (CreateSinkResponseObject, error) {
	b := *req.Body
	var creator *governance.Author
	if author := authorOf(ctx); author.Member != nil || author.Token != nil {
		creator = &author
	}
	draft := sinks.Draft{Input: sinks.Input{
		Name: b.Name, Kind: sinks.Kind(b.Kind), EventKinds: eventKindsIn(b.EventKinds),
		Environments: valueOr(b.Environments, nil), BatchSize: explicit(b.BatchSize),
		FlushInterval: flushOf(explicit(b.FlushInterval)),
	}}
	config, secret := configIn(b.Clickhouse, b.ObjectStorage, b.Webhook)
	if config != nil {
		draft.Config = *config
	}
	// Only the sub-object of the sink's own kind has a secret to seal; one
	// of another kind is refused, and its secret is not a reason to refuse
	// the sink a second time.
	if draft.Kind == sinks.KindClickHouse || draft.Kind == sinks.KindObjectStorage {
		draft.Secret = secret
	}
	created, err := a.h.CreateSink.Handle(ctx, appsinks.CreateCommand{
		Project: projectID(ctx), Draft: draft, CreatedBy: creator, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return CreateSink201JSONResponse{Body: sinkWithSecret(created), Headers: CreateSink201ResponseHeaders{ETag: etag(created.Sink.Version)}}, nil
}

// GetSink answers a sink.
func (a *api) GetSink(ctx context.Context, req GetSinkRequestObject) (GetSinkResponseObject, error) {
	s, err := a.h.GetSink.Handle(ctx, projectID(ctx), id.FromBytes[sinks.Sink](req.Sink))
	if err != nil {
		return nil, err
	}
	return GetSink200JSONResponse{Body: sinkOut(s), Headers: GetSink200ResponseHeaders{ETag: etag(s.Version)}}, nil
}

// UpdateSink edits a sink.
func (a *api) UpdateSink(ctx context.Context, req UpdateSinkRequestObject) (UpdateSinkResponseObject, error) {
	expected, err := ifMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	b := *req.Body
	change := sinks.Change{
		Name: b.Name, EventKinds: patchKinds(b.EventKinds), Environments: b.Environments, BatchSize: patchInt(b.BatchSize),
		FlushInterval: patchSeconds(b.FlushInterval), Enabled: b.Enabled,
	}
	if b.Kind != nil {
		kind := sinks.Kind(*b.Kind)
		change.Kind = &kind
	}
	var secret string
	change.Config, secret = configIn(b.Clickhouse, b.ObjectStorage, b.Webhook)
	s, err := a.h.UpdateSink.Handle(ctx, appsinks.UpdateCommand{
		Project: projectID(ctx), Sink: id.FromBytes[sinks.Sink](req.Sink), Change: change, Secret: secret,
		Expected: expected, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return UpdateSink200JSONResponse{Body: sinkOut(s), Headers: UpdateSink200ResponseHeaders{ETag: etag(s.Version)}}, nil
}

// EnableSink enables a sink once its target passes the check.
func (a *api) EnableSink(ctx context.Context, req EnableSinkRequestObject) (EnableSinkResponseObject, error) {
	cmd := appsinks.EnableCommand{
		Project: projectID(ctx), Sink: id.FromBytes[sinks.Sink](req.Sink), Request: requestOf(ctx),
	}
	if req.Body != nil && req.Body.StartFrom != nil {
		cmd.StartFrom = req.Body.StartFrom
	}
	s, err := a.h.EnableSink.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return EnableSink200JSONResponse{Body: sinkOut(s), Headers: EnableSink200ResponseHeaders{ETag: etag(s.Version)}}, nil
}

// TestSinkConfig tests a configuration that is not saved.
func (a *api) TestSinkConfig(ctx context.Context, req TestSinkConfigRequestObject) (TestSinkConfigResponseObject, error) {
	b := *req.Body
	draft := sinks.Draft{Input: sinks.Input{
		Name: b.Name, Kind: sinks.Kind(b.Kind), EventKinds: eventKindsIn(b.EventKinds),
		Environments: valueOr(b.Environments, nil), BatchSize: explicit(b.BatchSize),
		FlushInterval: flushOf(explicit(b.FlushInterval)),
	}}
	config, secret := configIn(b.Clickhouse, b.ObjectStorage, b.Webhook)
	if config != nil {
		draft.Config = *config
	}
	if draft.Kind == sinks.KindClickHouse || draft.Kind == sinks.KindObjectStorage {
		draft.Secret = secret
	}
	result, err := a.h.TestSinkConfig.Handle(ctx, appsinks.TestConfigCommand{Project: projectID(ctx), Draft: draft})
	if err != nil {
		return nil, err
	}
	return TestSinkConfig200JSONResponse(testResultOut(result)), nil
}

// TestSink tests a saved sink, or an edit of its configuration.
func (a *api) TestSink(ctx context.Context, req TestSinkRequestObject) (TestSinkResponseObject, error) {
	cmd := appsinks.TestCommand{Project: projectID(ctx), Sink: id.FromBytes[sinks.Sink](req.Sink), Request: requestOf(ctx)}
	if b := req.Body; b != nil {
		cmd.Config, cmd.Secret = configIn(b.Clickhouse, b.ObjectStorage, b.Webhook)
	}
	result, err := a.h.TestSink.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return TestSink200JSONResponse(testResultOut(result)), nil
}

// testResultOut answers what a test found.
func testResultOut(t appsinks.Tested) SinkTestResult {
	out := SinkTestResult{Ok: t.OK, Warnings: nonNilStrings(t.Warnings)}
	if t.Failure != nil {
		r := t.Failure.Report()
		class := SinkFailureClass(r.Class)
		out.Class, out.Status, out.Code = &class, r.Status, r.Code
	}
	return out
}

// SampleSink sends sample rows to a sink's target.
func (a *api) SampleSink(ctx context.Context, req SampleSinkRequestObject) (SampleSinkResponseObject, error) {
	cmd := appsinks.SampleCommand{Project: projectID(ctx), Sink: id.FromBytes[sinks.Sink](req.Sink), Request: requestOf(ctx)}
	if req.Body != nil {
		cmd.Kinds = eventKindsIn(req.Body.EventKinds)
	}
	sampled, err := a.h.SampleSink.Handle(ctx, cmd)
	if err != nil {
		return nil, err
	}
	out := SinkSampleResult{Results: make([]SinkSampleKindResult, len(sampled))}
	for i, v := range sampled {
		out.Results[i] = SinkSampleKindResult{EventKind: string(v.EventKind), Ok: v.Failure == nil}
		if v.Failure != nil {
			r := v.Failure.Report()
			class := SinkFailureClass(r.Class)
			out.Results[i].Class, out.Results[i].Status, out.Results[i].Code = &class, r.Status, r.Code
		}
	}
	return SampleSink200JSONResponse(out), nil
}

// ReplaySink sends a sink's history again from a time.
func (a *api) ReplaySink(ctx context.Context, req ReplaySinkRequestObject) (ReplaySinkResponseObject, error) {
	b := *req.Body
	s, err := a.h.ReplaySink.Handle(ctx, appsinks.ReplayCommand{
		Project: projectID(ctx), Sink: id.FromBytes[sinks.Sink](req.Sink), From: b.From, To: b.To,
		Kinds: eventKindsIn(b.EventKinds), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return ReplaySink200JSONResponse{Body: sinkOut(s), Headers: ReplaySink200ResponseHeaders{ETag: etag(s.Version)}}, nil
}

// DeleteSink deletes a sink.
func (a *api) DeleteSink(ctx context.Context, req DeleteSinkRequestObject) (DeleteSinkResponseObject, error) {
	expected, err := ifMatch(req.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	err = a.h.DeleteSink.Handle(ctx, appsinks.DeleteCommand{
		Project: projectID(ctx), Sink: id.FromBytes[sinks.Sink](req.Sink), Expected: expected, Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return DeleteSink204Response{}, nil
}

// RotateSinkSecret issues a webhook sink a new secret and answers it, this
// once.
func (a *api) RotateSinkSecret(ctx context.Context, req RotateSinkSecretRequestObject) (RotateSinkSecretResponseObject, error) {
	rotated, err := a.h.RotateSinkSecret.Handle(ctx, appsinks.RotateCommand{
		Project: projectID(ctx), Sink: id.FromBytes[sinks.Sink](req.Sink), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return RotateSinkSecret200JSONResponse{
		Body: sinkWithSecret(rotated), Headers: RotateSinkSecret200ResponseHeaders{ETag: etag(rotated.Sink.Version)},
	}, nil
}

// configIn returns the configuration a request body carries, with every
// sub-object it names, and the secret of the one that has a secret; nil
// when it names none. Absent properties take the defaults of a
// configuration written whole.
func configIn(ch *SinkClickHouseInput, os *SinkObjectStorageInput, wh *SinkWebhookInput) (*sinks.Config, string) {
	if ch == nil && os == nil && wh == nil {
		return nil, ""
	}
	var c sinks.Config
	var secret string
	if ch != nil {
		c.ClickHouse = &sinks.ClickHouse{
			URL: valueOf(ch.Url), Database: valueOf(ch.Database), TablePrefix: valueOf(ch.TablePrefix),
			Username: valueOf(ch.Username), TLSSkipVerify: valueOr(ch.TlsSkipVerify, false),
			CreateTables: valueOr(ch.CreateTables, true), Settings: valueOr(ch.Settings, nil),
		}
		secret = valueOf(ch.Password)
	}
	if os != nil {
		c.ObjectStorage = &sinks.ObjectStorage{
			Endpoint: valueOf(os.Endpoint), Region: valueOf(os.Region), Bucket: valueOf(os.Bucket),
			Prefix: valueOf(os.Prefix), AccessKeyID: valueOf(os.AccessKeyId), PathStyle: valueOr(os.PathStyle, false),
			Format: valueOf(os.Format), Compression: valueOf(os.Compression),
		}
		secret = valueOf(os.SecretAccessKey)
	}
	if wh != nil {
		c.Webhook = &sinks.Webhook{URL: valueOf(wh.Url), Headers: valueOr(wh.Headers, nil)}
	}
	return &c, secret
}

func eventKindsIn(in *[]string) []sinks.EventKind {
	if in == nil {
		return nil
	}
	return kindsOf(*in)
}

func patchKinds(in *[]string) *[]sinks.EventKind {
	if in == nil {
		return nil
	}
	kinds := kindsOf(*in)
	return &kinds
}

func kindsOf(in []string) []sinks.EventKind {
	out := make([]sinks.EventKind, len(in))
	for i, k := range in {
		out[i] = sinks.EventKind(k)
	}
	return out
}

func flushOf(n int) time.Duration { return time.Duration(n) * time.Second }

func patchSeconds(n *int) *time.Duration {
	if n == nil {
		return nil
	}
	d := flushOf(explicit(n))
	return &d
}

// explicit returns the batching value a request named, or 0, which the
// domain reads as the kind's default, when it named none. A value below 1 is
// passed on as -1 so that it is refused instead of read as absence.
func explicit(n *int) int {
	switch {
	case n == nil:
		return 0
	case *n < 1:
		return -1
	}
	return *n
}

func patchInt(n *int) *int {
	if n == nil {
		return nil
	}
	v := explicit(n)
	return &v
}

// sinkWithSecret answers a sink with the secret just issued to it, when it has
// one.
func sinkWithSecret(issued appsinks.Created) SinkWithSecret {
	s := sinkOut(issued.Sink)
	out := SinkWithSecret{
		Id: s.Id, Name: s.Name, Kind: s.Kind, Clickhouse: s.Clickhouse, ObjectStorage: s.ObjectStorage, Webhook: s.Webhook,
		EventKinds: s.EventKinds, Environments: s.Environments, BatchSize: s.BatchSize, FlushInterval: s.FlushInterval,
		Enabled: s.Enabled, Health: s.Health, AtRisk: s.AtRisk, LagSeconds: s.LagSeconds, DeadLetters: s.DeadLetters,
		Gaps: s.Gaps, StoppedAt: s.StoppedAt, Warnings: s.Warnings, Streams: s.Streams, CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt, Version: s.Version,
	}
	if value := issued.Secret.Reveal(); value != "" {
		out.Secret = &value
	}
	return out
}

// sinkOut answers s, its secrets masked.
func sinkOut(s sinks.Sink) Sink {
	out := Sink{
		Id: openapi_types.UUID(s.ID.Bytes()), Name: s.Name, Kind: SinkKind(s.Kind),
		EventKinds: make([]SinkEventKind, len(s.EventKinds)), Environments: nonNilStrings(s.Environments),
		BatchSize: s.BatchSize, FlushInterval: int(s.FlushInterval / time.Second), Enabled: s.Enabled,
		Health: SinkHealth(s.Status), AtRisk: s.AtRiskSince != nil, StoppedAt: s.StoppedAt,
		DeadLetters: s.DeadLetters, Gaps: s.Gaps, Warnings: nonNilStrings(s.Warnings), Streams: make([]SinkStream, len(s.Streams)),
		CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, Version: s.Version,
	}
	if s.Lag != nil {
		seconds := int64(s.Lag.Seconds())
		out.LagSeconds = &seconds
	}
	for i, st := range s.Streams {
		out.Streams[i] = SinkStream{EventKind: SinkEventKind(st.EventKind), LastDeliveredAt: st.LastDeliveredAt}
		if f := st.LastFailure; f != nil {
			r := f.Report()
			out.Streams[i].LastFailure = &SinkFailure{Class: SinkFailureClass(r.Class), Status: r.Status, Code: r.Code, At: f.At}
		}
	}
	for i, k := range s.EventKinds {
		out.EventKinds[i] = SinkEventKind(k)
	}
	switch c := s.Config; {
	case c.ClickHouse != nil:
		out.Clickhouse = &SinkClickHouse{
			Url: c.ClickHouse.URL, Database: c.ClickHouse.Database, TablePrefix: c.ClickHouse.TablePrefix,
			Username: c.ClickHouse.Username, Password: sinks.Masked, TlsSkipVerify: c.ClickHouse.TLSSkipVerify,
			CreateTables: c.ClickHouse.CreateTables, Settings: nonNilMap(c.ClickHouse.Settings),
		}
	case c.ObjectStorage != nil:
		o := c.ObjectStorage
		out.ObjectStorage = &SinkObjectStorage{
			Endpoint: o.Endpoint, Region: o.Region, Bucket: o.Bucket, Prefix: o.Prefix, AccessKeyId: o.AccessKeyID,
			SecretAccessKey: sinks.Masked, PathStyle: o.PathStyle, Format: SinkObjectStorageFormat(o.Format),
			Compression: SinkObjectStorageCompression(o.Compression),
		}
	case c.Webhook != nil:
		out.Webhook = &SinkWebhook{Url: c.Webhook.URL, Headers: nonNilMap(c.Webhook.Headers)}
	}
	return out
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nonNilMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// ResumeSink starts a stopped sink again.
func (a *api) ResumeSink(ctx context.Context, req ResumeSinkRequestObject) (ResumeSinkResponseObject, error) {
	s, err := a.h.ResumeSink.Handle(ctx, appsinks.ResumeCommand{
		Project: projectID(ctx), Sink: id.FromBytes[sinks.Sink](req.Sink), Request: requestOf(ctx),
	})
	if err != nil {
		return nil, err
	}
	return ResumeSink200JSONResponse{Body: sinkOut(s), Headers: ResumeSink200ResponseHeaders{ETag: etag(s.Version)}}, nil
}

// ListSinkDeadLetters answers a sink's dead letters.
func (a *api) ListSinkDeadLetters(ctx context.Context, req ListSinkDeadLettersRequestObject) (ListSinkDeadLettersResponseObject, error) {
	list, err := a.h.ListSinkDeadLetters.Handle(ctx, projectID(ctx), id.FromBytes[sinks.Sink](req.Sink))
	if err != nil {
		return nil, err
	}
	return ListSinkDeadLetters200JSONResponse{Items: each(list, deadLetterOut)}, nil
}

// RedeliverSinkDeadLetter sends a dead letter's rows to the target again.
func (a *api) RedeliverSinkDeadLetter(ctx context.Context, req RedeliverSinkDeadLetterRequestObject) (RedeliverSinkDeadLetterResponseObject, error) {
	dl, err := a.h.RedeliverSinkDeadLetter.Handle(ctx, deadLetterCommand(ctx, req.Sink, req.DeadLetter))
	if err != nil {
		return nil, err
	}
	return RedeliverSinkDeadLetter200JSONResponse(deadLetterOut(dl)), nil
}

// DiscardSinkDeadLetter gives a dead letter up.
func (a *api) DiscardSinkDeadLetter(ctx context.Context, req DiscardSinkDeadLetterRequestObject) (DiscardSinkDeadLetterResponseObject, error) {
	if err := a.h.DiscardSinkDeadLetter.Handle(ctx, deadLetterCommand(ctx, req.Sink, req.DeadLetter)); err != nil {
		return nil, err
	}
	return DiscardSinkDeadLetter204Response{}, nil
}

func deadLetterCommand(ctx context.Context, sink SinkID, dl DeadLetterID) appsinks.DeadLetterCommand {
	return appsinks.DeadLetterCommand{
		Project: projectID(ctx), Sink: id.FromBytes[sinks.Sink](sink), DeadLetter: id.FromBytes[sinks.DeadLetter](dl),
		Request: requestOf(ctx),
	}
}

// deadLetterOut answers dl: the batch by its size and times, and why it was
// last rejected by class, status and code.
func deadLetterOut(dl sinks.DeadLetter) SinkDeadLetter {
	r := dl.LastFailure.Report()
	return SinkDeadLetter{
		Id: openapi_types.UUID(dl.ID.Bytes()), EventKind: SinkEventKind(dl.EventKind), Rows: dl.Rows,
		FirstReceivedAt: dl.FirstReceivedAt, LastReceivedAt: dl.LastReceivedAt, Attempts: dl.Attempts,
		LastFailure: SinkFailure{Class: SinkFailureClass(r.Class), Status: r.Status, Code: r.Code, At: dl.LastFailure.At},
		Status:      SinkDeadLetterStatus(dl.Status), CreatedAt: dl.CreatedAt, RedeliveredAt: dl.RedeliveredAt,
	}
}
