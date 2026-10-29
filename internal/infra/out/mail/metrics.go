package mail

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/domain/access"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
)

// Metrics counts the mails sent as flagnetic_mail_sent_total{kind,result}.
type Metrics struct {
	sent *prometheus.CounterVec
}

// NewMetrics registers the counter on reg.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	sent := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metrics.Namespace, Subsystem: "mail", Name: "sent_total",
		Help: "Mails the replica tried to send, by kind and result: sent, the class of the failure " +
			"(connection, tls, authentication, rejected), or error for a failure before the SMTP server was reached.",
	}, []string{"kind", "result"})
	reg.MustRegister(sent)
	return &Metrics{sent: sent}
}

// Count counts one mail of kind with result.
func (m *Metrics) Count(kind access.MailKind, result string) {
	m.sent.WithLabelValues(string(kind), result).Inc()
}
