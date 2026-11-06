package identity

import (
	"context"

	"github.com/azverinus/flagnetic/internal/domain/identity"
)

// RotateCertificate starts the rotation of a SAML provider's SP certificate.
type RotateCertificate struct{ d ProviderDeps }

// NewRotateCertificate returns the handler.
func NewRotateCertificate(d ProviderDeps) *RotateCertificate { return &RotateCertificate{d: d} }

// Handle generates the provider's next key pair, seals its private key and
// stores it beside the current pair, and records
// auth_provider.certificate_rotation_started with the certificates' fingerprints
// and never a key. A provider that is not SAML is refused 422 not_saml, one
// whose rotation is pending 409 rotation_pending.
func (h *RotateCertificate) Handle(ctx context.Context, cmd ProviderCommand) (identity.Provider, error) {
	// A refusal is judged before a key pair is made, which takes a moment, and
	// again under the provider's lock, which decides.
	now := h.d.now()
	current, err := providerOf(ctx, h.d.Store, cmd.Slug)
	if err != nil {
		return identity.Provider{}, err
	}
	if _, err = current.StartRotation(identity.SPKey{}, now); err != nil {
		return identity.Provider{}, err
	}
	next, err := sealSPKey(h.d.SPKeys, h.d.Sealer, now)
	if err != nil {
		return identity.Provider{}, err
	}
	var out identity.Provider
	err = h.d.Tx.Within(ctx, func(ctx context.Context) error {
		before, lockErr := lockProvider(ctx, h.d.Store, cmd.Slug)
		if lockErr != nil {
			return lockErr
		}
		after, startErr := before.StartRotation(next, now)
		if startErr != nil {
			return startErr
		}
		if storeErr := h.d.Store.UpdateProvider(ctx, after); storeErr != nil {
			return storeErr
		}
		out = after
		return h.d.record(ctx, now, cmd.Request, "auth_provider.certificate_rotation_started", &before, &after)
	})
	return out, err
}

// CompleteCertificateRotation completes the rotation of a SAML provider's SP
// certificate.
type CompleteCertificateRotation struct{ d ProviderDeps }

// NewCompleteCertificateRotation returns the handler.
func NewCompleteCertificateRotation(d ProviderDeps) *CompleteCertificateRotation {
	return &CompleteCertificateRotation{d: d}
}

// Handle makes the next key pair current, destroys the retired private key,
// and records auth_provider.certificate_rotation_completed. A provider that is
// not SAML is refused 422 not_saml, one with no rotation pending 409
// no_rotation_pending.
func (h *CompleteCertificateRotation) Handle(ctx context.Context, cmd ProviderCommand) (identity.Provider, error) {
	now := h.d.now()
	var out identity.Provider
	err := h.d.Tx.Within(ctx, func(ctx context.Context) error {
		before, err := lockProvider(ctx, h.d.Store, cmd.Slug)
		if err != nil {
			return err
		}
		after, err := before.CompleteRotation(now)
		if err != nil {
			return err
		}
		if err = h.d.Store.UpdateProvider(ctx, after); err != nil {
			return err
		}
		out = after
		return h.d.record(ctx, now, cmd.Request, "auth_provider.certificate_rotation_completed", &before, &after)
	})
	return out, err
}
