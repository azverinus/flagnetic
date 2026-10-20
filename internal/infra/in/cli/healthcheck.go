package cli

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/azverinus/flagnetic/internal/infra/config"
)

// healthcheckTimeout bounds the readiness request, within the few seconds a
// container runtime usually allows a health check.
const healthcheckTimeout = 2 * time.Second

// healthcheck asks the replica listening on the configured address whether it
// is ready, so a container without a shell or curl can have a health check.
// It exits 0 when /readyz answers 200 and 1 otherwise.
func healthcheck(ctx context.Context, cfg config.Config, log *slog.Logger) int {
	host, port, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil {
		return failHealth(log, err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	url := "http://" + net.JoinHostPort(host, port) + "/readyz"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return failHealth(log, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return failHealth(log, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Warn("not ready", "status", resp.StatusCode)
		return exitFailure
	}
	return exitOK
}

func failHealth(log *slog.Logger, err error) int {
	log.Warn("not ready", "error", err.Error())
	return exitFailure
}
