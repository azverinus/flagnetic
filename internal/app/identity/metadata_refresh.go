package identity

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/identity"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// MetadataRefreshStore is what refreshing SAML identity provider metadata
// reads and writes beside the providers themselves.
type MetadataRefreshStore interface {
	// TryLockMetadataRefresh takes, until release is called, the lock that
	// lets one replica refresh at a time; held is false when another holds it.
	TryLockMetadataRefresh(ctx context.Context) (release func(), held bool, err error)
	// MetadataRefreshCandidates returns the enabled SAML providers whose
	// metadata comes from a URL and was read at or before cutoff.
	MetadataRefreshCandidates(ctx context.Context, cutoff time.Time) ([]identity.Provider, error)
	// UpdateIDPMetadata stores the identity provider metadata, the time it
	// was read, the update time and the version of p.
	UpdateIDPMetadata(ctx context.Context, p identity.Provider) error
}

// MetadataRefreshDeps are what the metadata refresh runs on. Providers is the
// ProviderStore of Deps; Deps.SAML fetches and reads the metadata.
type MetadataRefreshDeps struct {
	Deps   ProviderDeps
	Store  MetadataRefreshStore
	Logger *slog.Logger
}

// metadataRefreshActor is who the audit log names for a refresh: no member
// asked for it.
var metadataRefreshActor = governance.Actor{Kind: governance.ActorSystem, ID: "saml_metadata_refresh", Name: "SAML metadata refresh"}

// MetadataRefreshed counts what one round of the refresh did.
type MetadataRefreshed struct {
	// Changed providers had their certificates or single sign-on address
	// replaced; Unchanged ones were read and said the same; Refused ones named
	// another entity; Failed ones could not be fetched or read.
	Changed, Unchanged, Refused, Failed int
}

// RefreshMetadata reads again, once a day, the metadata of the SAML providers
// whose identity provider publishes it at a URL, so the certificates it rotates
// reach the instance without anyone swapping the identity provider silently.
type RefreshMetadata struct{ d MetadataRefreshDeps }

// NewRefreshMetadata returns the handler.
func NewRefreshMetadata(d MetadataRefreshDeps) *RefreshMetadata { return &RefreshMetadata{d: d} }

// Handle refreshes each enabled SAML provider whose metadata comes from a URL
// and was read a day ago or more, under a lock only one replica holds: another
// replica's round does nothing. A provider whose metadata cannot be fetched or
// read keeps what it has, is logged, and is tried again in the next round. A
// provider whose metadata names another entity is refused: logged, and recorded
// as auth_provider.metadata_refresh_refused, every round until an instance
// admin deals with it. New certificates or a new single sign-on address are
// applied and recorded as auth_provider.metadata_refreshed; metadata that says
// what the provider holds only moves the time it was read.
func (h *RefreshMetadata) Handle(ctx context.Context) (MetadataRefreshed, error) {
	var done MetadataRefreshed
	release, held, err := h.d.Store.TryLockMetadataRefresh(ctx)
	if err != nil || !held {
		return done, err
	}
	defer release()
	due, err := h.d.Store.MetadataRefreshCandidates(ctx, h.d.Deps.now().Add(-identity.MetadataRefreshAfter))
	if err != nil {
		return done, err
	}
	var failed []error
	for _, p := range due {
		outcome, err := h.refresh(ctx, p)
		switch {
		case err != nil && ctx.Err() != nil:
			return done, errors.Join(append(failed, err)...)
		case err != nil:
			failed = append(failed, fmt.Errorf("refreshing the metadata of %s: %w", p.Slug, err))
		case outcome == outcomeFailed:
			done.Failed++
		case outcome == outcomeUnchanged:
			done.Unchanged++
		case outcome == outcomeRefused:
			done.Refused++
		case outcome == outcomeChanged:
			done.Changed++
		}
	}
	return done, errors.Join(failed...)
}

type refreshOutcome int

const (
	// outcomeSkipped: the provider changed under the round and is not due any more.
	outcomeSkipped refreshOutcome = iota
	outcomeFailed
	outcomeUnchanged
	outcomeChanged
	outcomeRefused
)

// refresh fetches the metadata of p, outside any unit of work so the provider's
// row is not held while an identity provider answers slowly, then judges and
// stores it under the row's lock. An error is a failure of the instance's own
// storage; one of the identity provider's is logged and answered as
// outcomeFailed.
func (h *RefreshMetadata) refresh(ctx context.Context, p identity.Provider) (refreshOutcome, error) {
	d := h.d.Deps
	raw, err := d.SAML.Fetch(ctx, p.SAML.MetadataURL)
	var fetched identity.IDPMetadata
	if err == nil {
		fetched, err = d.SAML.Parse(raw)
	}
	if err != nil {
		if ctx.Err() != nil {
			return outcomeFailed, ctx.Err()
		}
		h.d.Logger.WarnContext(ctx, "the identity provider's metadata could not be refreshed; the previous metadata stays in use and the refresh is tried again",
			"provider", p.Slug, "error", clip(err.Error(), maxFailureBytes))
		return outcomeFailed, nil
	}
	now := d.now()
	outcome := outcomeSkipped
	err = d.Tx.Within(ctx, func(ctx context.Context) error {
		before, lockErr := lockProvider(ctx, d.Store, p.Slug)
		if errors.Is(lockErr, errProviderNotFound) {
			return nil
		}
		if lockErr != nil {
			return lockErr
		}
		// An edit or a disabling made while the metadata was fetched decides
		// over a fetch of what the provider used to be.
		if !before.MetadataDue(now) || before.SAML.MetadataURL != p.SAML.MetadataURL {
			return nil
		}
		after, result := before.RefreshIDPMetadata(fetched, now)
		req := governance.Request{Actor: metadataRefreshActor}
		switch result {
		case identity.RefreshRefused:
			outcome = outcomeRefused
			return d.recordRefusedRefresh(ctx, now, req, before, fetched.EntityID)
		case identity.RefreshChanged:
			outcome = outcomeChanged
			if storeErr := h.d.Store.UpdateIDPMetadata(ctx, after); storeErr != nil {
				return storeErr
			}
			return d.record(ctx, now, req, "auth_provider.metadata_refreshed", &before, &after)
		default:
			outcome = outcomeUnchanged
			return h.d.Store.UpdateIDPMetadata(ctx, after)
		}
	})
	if err != nil {
		return outcomeFailed, err
	}
	if outcome == outcomeRefused {
		h.d.Logger.WarnContext(ctx, "the identity provider's metadata names another entity than the provider's, so it was not applied; "+
			"an instance admin decides whether the provider moved or its metadata address is wrong",
			"provider", p.Slug, "entity_id", p.SAML.IDP.EntityID, "offered_entity_id", fetched.EntityID)
	}
	return outcome, nil
}

// recordRefusedRefresh records that offered, the entity the metadata named, was
// not applied to p.
func (d ProviderDeps) recordRefusedRefresh(ctx context.Context, now time.Time, req governance.Request, p identity.Provider, offered string) error {
	entry, err := governance.NewEntry(id.New[governance.Entry](d.IDs), now, req, governance.Record{
		Action: "auth_provider.metadata_refresh_refused",
		Entity: governance.Entity{Kind: "auth_provider", ID: p.ID.String(), Key: p.Slug},
		Before: map[string]any{"idp_entity_id": p.SAML.IDP.EntityID},
		After:  map[string]any{"idp_entity_id": p.SAML.IDP.EntityID, "refused_idp_entity_id": offered},
	})
	if err != nil {
		return err
	}
	return d.Audit.Record(ctx, entry)
}
