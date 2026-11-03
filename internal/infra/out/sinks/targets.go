package sinks

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/google/uuid"

	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/sinks"
)

// Targets are the targets of the kinds of sink that can be delivered to, each
// calling its system through a client guarded for its kind.
type Targets struct {
	webhook *Webhook
}

var _ appsinks.Targets = (*Targets)(nil)

// NewTargets returns the targets, whose webhook receivers the operator's
// policy may allow to be on private networks.
func NewTargets(policy governance.TargetPolicy, resolver Resolver) *Targets {
	return &Targets{webhook: NewWebhook(NewClient(sinks.KindWebhook, Options{Policy: policy, Resolver: resolver}))}
}

// For returns the target of kind k.
func (t *Targets) For(k sinks.Kind) (appsinks.Target, bool) {
	if k == sinks.KindWebhook {
		return t.webhook, true
	}
	return nil, false
}

// Webhook delivers batches to a webhook receiver as signed POSTs, verified as a
// webhook subscription's calls are.
type Webhook struct{ client *http.Client }

var _ appsinks.Target = (*Webhook)(nil)

// NewWebhook returns the target calling receivers with client.
func NewWebhook(client *http.Client) *Webhook { return &Webhook{client: client} }

// Probe shows whether the receiver answers a signed call that carries no
// events with a 2xx. A webhook receiver has nothing to prepare.
func (w *Webhook) Probe(ctx context.Context, c appsinks.Call, _ bool) appsinks.Probed {
	return appsinks.Probed{Failure: w.Deliver(ctx, c, appsinks.Delivery{ID: "test_" + uuid.NewString(), Test: true})}
}

// Deliver posts the batch, signed, and answers why the receiver did not
// acknowledge it with a 2xx: a request timeout, too many requests and a server
// error are a status, which a retry may cure; any other answer is a rejection.
// What the receiver wrote is never read.
func (w *Webhook) Deliver(ctx context.Context, c appsinks.Call, d appsinks.Delivery) *sinks.Failure {
	kind := string(d.Kind)
	if d.Test {
		kind = "test"
	}
	body, err := sinks.WebhookBody(c.Sink.ID, c.ProjectKey, d.ID, kind, d.Sample, d.Rows)
	if err != nil {
		return &sinks.Failure{Class: sinks.ClassConnect}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Sink.Config.Webhook.URL, bytes.NewReader(body))
	if err != nil {
		return &sinks.Failure{Class: sinks.ClassConnect}
	}
	// The member's headers go first, so that none of them can replace what
	// the receiver verifies the call by.
	for k, v := range c.Sink.Config.Webhook.Headers {
		req.Header.Set(k, v)
	}
	timestamp := governance.SignatureTimestamp(d.Now)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Flagnetic-Sink")
	req.Header.Set(governance.HeaderDelivery, d.ID)
	req.Header.Set(governance.HeaderTimestamp, timestamp)
	req.Header.Set(governance.HeaderSignature, governance.Sign(timestamp, body, c.Secrets...))
	res, err := w.client.Do(req)
	if err != nil {
		f := ClassifyError(err)
		return &f
	}
	defer func() { _ = res.Body.Close() }()
	// What the receiver answers is read to let the connection be reused, and
	// dropped: no sink keeps a target's words.
	defer func() { _, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10)) }()
	if f, failed := ClassifyResponse(sinks.KindWebhook, res); failed {
		return &f
	}
	return nil
}
