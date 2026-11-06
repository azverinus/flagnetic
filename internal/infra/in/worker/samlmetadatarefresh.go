package worker

import (
	"context"
	"time"

	appidentity "github.com/azverinus/flagnetic/internal/app/identity"
)

// SAMLMetadataRefreshName names the SAML metadata refresh worker in logs,
// metrics and Tick.
const SAMLMetadataRefreshName = "saml_metadata_refresh"

// SAMLMetadataRefresh returns the worker reading again, every hour, the
// metadata of the SAML providers whose identity provider publishes it at a URL
// and was read a day ago, so a certificate the identity provider rotates
// reaches the instance before the old one expires. An hourly round bounds how
// late a failed fetch is tried again to an hour; one replica refreshes at a
// time.
func SAMLMetadataRefresh(h *appidentity.RefreshMetadata) Worker {
	return Worker{
		Name: SAMLMetadataRefreshName, Interval: time.Hour,
		// Each fetch is bounded at ten seconds, so a few providers fit.
		Timeout: 5 * time.Minute,
		Round: func(ctx context.Context) error {
			_, err := h.Handle(ctx)
			return err
		},
	}
}
