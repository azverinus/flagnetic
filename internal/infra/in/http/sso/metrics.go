package sso

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/infra/metrics"
)

// kindOIDC is the kind of the sign-ins counted here.
const kindOIDC = "oidc"

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

func (m *Metrics) count(result string) {
	if m != nil {
		m.signIns.WithLabelValues(kindOIDC, result).Inc()
	}
}
