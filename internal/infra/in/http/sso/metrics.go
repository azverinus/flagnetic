package sso

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/infra/metrics"
)

// Metrics counts sign-ins as flagnetic_sso_sign_ins_total{kind,result}.
type Metrics struct {
	signIns *prometheus.CounterVec
}

// NewMetrics registers the counter on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	signIns := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metrics.Namespace, Subsystem: "sso", Name: "sign_ins_total",
		Help: "Single sign-ons that ended, by kind of provider and result: succeeded, or the code the sign-in page was " +
			"told (sso_failed, idp_error, member_not_found, member_deactivated, attempt_mismatch).",
	}, []string{"kind", "result"})
	reg.MustRegister(signIns)
	return &Metrics{signIns: signIns}
}

// count counts a sign-in through a provider of kind; "unknown" when it failed
// before the provider was.
func (m *Metrics) count(kind string, result string) {
	if kind == "" {
		kind = "unknown"
	}
	if m != nil {
		m.signIns.WithLabelValues(kind, result).Inc()
	}
}
