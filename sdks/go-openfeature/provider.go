package openfeature

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	of "github.com/open-feature/go-sdk/openfeature"

	flagnetic "github.com/azverinus/flagnetic/sdks/go"
)

// providerName is this provider's OpenFeature Metadata.Name.
const providerName = "flagnetic"

// eventBuffer bounds the queue of provider events (ready,
// configuration changed, stale, error) waiting for the OpenFeature SDK's
// drain goroutine; a slow or absent drain drops the new event rather than
// blocking the flagnetic client's background loop.
const eventBuffer = 16

// Tracking event names every Flagnetic provider accepts.
const (
	trackExposure = "exposure"
	trackAlias    = "alias"
)

// defaultLog is flagnetic's own default logger (SlogLogger(nil,
// slog.LevelWarn)), kept so the provider's own logger, installed to observe
// key_revoked, still logs everything the client's default would have.
var defaultLog = flagnetic.SlogLogger(nil, slog.LevelWarn)

// Provider is a FeatureProvider over a *flagnetic.Client that New builds
// and owns: it implements FeatureProvider, the context-aware StateHandler,
// EventHandler and Tracker. Construct it with New; Client returns the
// wrapped client for exposure and alias calls OpenFeature's Track cannot
// reach.
type Provider struct {
	client       *flagnetic.Client
	unitType     string
	readyTimeout time.Duration

	closed atomic.Bool
	events chan of.Event

	unsubChange func()
	unsubStage  func()
}

// interface guards.
var (
	_ of.FeatureProvider          = (*Provider)(nil)
	_ of.ContextAwareStateHandler = (*Provider)(nil)
	_ of.EventHandler             = (*Provider)(nil)
	_ of.Tracker                  = (*Provider)(nil)
)

// New constructs a flagnetic.Client for sdkKey and baseURL and wraps it in
// a Provider. WithClientOptions forwards options to the client, including
// WithOffline for tests. New fails exactly when flagnetic.New would.
func New(sdkKey, baseURL string, opts ...Option) (*Provider, error) {
	cfg := config{unitType: defaultUnitType, readyTimeout: defaultReadyTimeout}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	p := &Provider{
		unitType:     cfg.unitType,
		readyTimeout: cfg.readyTimeout,
		events:       make(chan of.Event, eventBuffer),
	}
	clientOpts := make([]flagnetic.Option, 0, len(cfg.clientOptions)+1)
	clientOpts = append(clientOpts, flagnetic.WithLogger(p.observe))
	clientOpts = append(clientOpts, cfg.clientOptions...)
	client, err := flagnetic.New(sdkKey, baseURL, clientOpts...)
	if err != nil {
		return nil, err
	}
	p.client = client
	p.unsubChange = client.OnChange(p.onChange)
	p.unsubStage = client.OnStage(p.onStage)
	return p, nil
}

// Client returns the wrapped flagnetic.Client, so the application can call
// Exposure and Alias directly where OpenFeature's Track has no reach.
func (p *Provider) Client() *flagnetic.Client {
	return p.client
}

// Metadata returns the provider's name.
func (p *Provider) Metadata() of.Metadata {
	return of.Metadata{Name: providerName}
}

// BooleanEvaluation serves a boolean flag through the wrapped client.
func (p *Provider) BooleanEvaluation(ctx context.Context, flag string, defaultValue bool, flatCtx of.FlattenedContext) of.BoolResolutionDetail {
	d := p.client.Detail(ctx, p.context(flatCtx), flag, defaultValue)
	value, _ := d.Value.(bool)
	return of.BoolResolutionDetail{Value: value, ProviderResolutionDetail: resolution(d)}
}

// StringEvaluation serves a string flag through the wrapped client.
func (p *Provider) StringEvaluation(ctx context.Context, flag string, defaultValue string, flatCtx of.FlattenedContext) of.StringResolutionDetail {
	d := p.client.Detail(ctx, p.context(flatCtx), flag, defaultValue)
	value, _ := d.Value.(string)
	return of.StringResolutionDetail{Value: value, ProviderResolutionDetail: resolution(d)}
}

// FloatEvaluation serves a number flag through the wrapped client.
func (p *Provider) FloatEvaluation(ctx context.Context, flag string, defaultValue float64, flatCtx of.FlattenedContext) of.FloatResolutionDetail {
	d := p.client.Detail(ctx, p.context(flatCtx), flag, defaultValue)
	value, _ := d.Value.(float64)
	return of.FloatResolutionDetail{Value: value, ProviderResolutionDetail: resolution(d)}
}

// IntEvaluation serves a number flag through the wrapped client by the
// SDK's Int rule: an int64 default makes Detail read the flag as Int does,
// so a number that is not whole or does not fit in an int64 serves the
// default with TYPE_MISMATCH rather than a truncated value.
func (p *Provider) IntEvaluation(ctx context.Context, flag string, defaultValue int64, flatCtx of.FlattenedContext) of.IntResolutionDetail {
	d := p.client.Detail(ctx, p.context(flatCtx), flag, defaultValue)
	value, _ := d.Value.(int64)
	return of.IntResolutionDetail{Value: value, ProviderResolutionDetail: resolution(d)}
}

// ObjectEvaluation serves a JSON flag through the wrapped client, encoding
// defaultValue to JSON for the call and decoding what was served back to an
// any; on fallback it returns defaultValue itself, not a round trip through
// JSON.
func (p *Provider) ObjectEvaluation(ctx context.Context, flag string, defaultValue any, flatCtx of.FlattenedContext) of.InterfaceResolutionDetail {
	raw, err := json.Marshal(defaultValue)
	if err != nil {
		return of.InterfaceResolutionDetail{
			Value: defaultValue,
			ProviderResolutionDetail: of.ProviderResolutionDetail{
				ResolutionError: of.NewGeneralResolutionError(fmt.Sprintf("flagnetic: default value: %v", err)),
				Reason:          of.ErrorReason,
			},
		}
	}
	d := p.client.Detail(ctx, p.context(flatCtx), flag, json.RawMessage(raw))
	if d.Reason == flagnetic.ReasonFallback {
		return of.InterfaceResolutionDetail{Value: defaultValue, ProviderResolutionDetail: resolution(d)}
	}
	var value any
	if served, ok := d.Value.(json.RawMessage); ok {
		if err := json.Unmarshal(served, &value); err != nil {
			res := resolution(d)
			res.ResolutionError = of.NewGeneralResolutionError(fmt.Sprintf("flagnetic: decode served value: %v", err))
			res.Reason = of.ErrorReason
			return of.InterfaceResolutionDetail{Value: defaultValue, ProviderResolutionDetail: res}
		}
	}
	return of.InterfaceResolutionDetail{Value: value, ProviderResolutionDetail: resolution(d)}
}

// Hooks returns none: the Go OpenFeature SDK offers Track, so exposure and
// alias tracking is Track, never an after-hook as in OpenFeature SDKs
// without one.
func (p *Provider) Hooks() []of.Hook {
	return nil
}

// Track maps "exposure" to Exposure(flag) and "alias" to Alias(...), the
// names every Flagnetic provider accepts. An event naming neither, or
// missing the attributes its mapping needs, does nothing.
func (p *Provider) Track(ctx context.Context, trackingEventName string, evalCtx of.EvaluationContext, details of.TrackingEventDetails) {
	switch trackingEventName {
	case trackExposure:
		flag, ok := details.Attribute("flag").(string)
		if !ok || flag == "" {
			return
		}
		p.client.Exposure(ctx, p.context(flatten(evalCtx)), flag)
	case trackAlias:
		fromUnitType, _ := details.Attribute("fromUnitType").(string)
		fromUnitID, _ := details.Attribute("fromUnitId").(string)
		toUnitType, _ := details.Attribute("toUnitType").(string)
		toUnitID, _ := details.Attribute("toUnitId").(string)
		if fromUnitType == "" || fromUnitID == "" || toUnitType == "" || toUnitID == "" {
			return
		}
		p.client.Alias(ctx, fromUnitType, fromUnitID, toUnitType, toUnitID)
	}
}

// Init waits for the wrapped client to reach ready, bounded by
// WithReadyTimeout, and never fails: a client at stage built_in is still a
// usable provider (the fallback chain serves built-in defaults). Prefer
// InitWithContext, what OpenFeature's context-aware Set* calls use, which
// waits on the caller's own context instead.
func (p *Provider) Init(_ of.EvaluationContext) error {
	ctx, cancel := context.WithTimeout(context.Background(), p.readyTimeout)
	defer cancel()
	p.client.Ready(ctx)
	return nil
}

// InitWithContext waits for the wrapped client to reach ready, or until ctx
// is done, and never fails, for the reason Init documents.
func (p *Provider) InitWithContext(ctx context.Context, _ of.EvaluationContext) error {
	p.client.Ready(ctx)
	return nil
}

// Shutdown closes the wrapped client, flushing queued exposures, aliases
// and the evaluation summary within its close timeout.
func (p *Provider) Shutdown() {
	_ = p.ShutdownWithContext(context.Background())
}

// ShutdownWithContext closes the wrapped client within ctx, flushing queued
// exposures, aliases and the evaluation summary.
func (p *Provider) ShutdownWithContext(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.closed.Store(true)
	if p.unsubChange != nil {
		p.unsubChange()
	}
	if p.unsubStage != nil {
		p.unsubStage()
	}
	return p.client.Close(ctx)
}

// EventChannel implements EventHandler: the OpenFeature SDK drains it for
// as long as this provider is registered.
func (p *Provider) EventChannel() <-chan of.Event {
	return p.events
}

// onChange fires CONFIGURATION_CHANGED with the changed keys.
func (p *Provider) onChange(changed []string) {
	p.emit(of.Event{
		ProviderName: providerName,
		EventType:    of.ProviderConfigChange,
		ProviderEventDetails: of.ProviderEventDetails{
			FlagChanges: changed,
		},
	})
}

// onStage fires STALE on entering stage last_good; other transitions are
// silent, ready is Init's job.
func (p *Provider) onStage(stage flagnetic.Stage) {
	if stage != flagnetic.StageLastGood {
		return
	}
	p.emit(of.Event{
		ProviderName: providerName,
		EventType:    of.ProviderStale,
		ProviderEventDetails: of.ProviderEventDetails{
			Message: "flagnetic: ruleset stage is last_good",
		},
	})
}

// observe is the client's logger: it logs exactly as flagnetic's own
// default would, and additionally fires PROVIDER_ERROR when the SDK key is
// revoked, the one case last_good
// alone does not distinguish from an ordinary fetch failure.
func (p *Provider) observe(level slog.Level, event flagnetic.Event, fields map[string]any) {
	defaultLog(level, event, fields)
	if event != flagnetic.EventKeyRevoked {
		return
	}
	p.emit(of.Event{
		ProviderName: providerName,
		EventType:    of.ProviderError,
		ProviderEventDetails: of.ProviderEventDetails{
			Message:   "flagnetic: sdk key revoked",
			ErrorCode: of.GeneralCode,
		},
	})
}

// emit queues e for the OpenFeature SDK's drain goroutine, dropping it
// rather than blocking when the queue is full or the provider has shut
// down: sends after Shutdown must not panic on a closed channel, so emit
// never closes p.events and instead gates on closed.
func (p *Provider) emit(e of.Event) {
	if p.closed.Load() {
		return
	}
	select {
	case p.events <- e:
	default:
	}
}
