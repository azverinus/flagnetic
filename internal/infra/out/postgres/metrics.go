package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/infra/metrics"
)

// PoolCollector exports a pool's statistics, so a saturated pool shows before
// requests start timing out: acquired close to max, and acquire waits
// growing.
type PoolCollector struct {
	pool *pgxpool.Pool

	acquired, idle, total, maxConns, acquires, emptyAcquires, acquireWait *prometheus.Desc
}

// NewPoolCollector returns a collector of pool's statistics.
func NewPoolCollector(pool *pgxpool.Pool) *PoolCollector {
	desc := func(name, help string) *prometheus.Desc {
		return prometheus.NewDesc(prometheus.BuildFQName(metrics.Namespace, "db_pool", name), help, nil, nil)
	}
	return &PoolCollector{
		pool:          pool,
		acquired:      desc("acquired_conns", "Connections in use."),
		idle:          desc("idle_conns", "Connections idle in the pool."),
		total:         desc("total_conns", "Connections open, in use, idle or being opened."),
		maxConns:      desc("max_conns", "Size of the pool."),
		acquires:      desc("acquires_total", "Connections acquired from the pool."),
		emptyAcquires: desc("empty_acquires_total", "Acquires that had to wait because no connection was idle."),
		acquireWait:   desc("acquire_wait_seconds_total", "Time spent waiting to acquire a connection."),
	}
}

// Describe sends the descriptions of the pool metrics.
func (c *PoolCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.acquired, c.idle, c.total, c.maxConns, c.acquires, c.emptyAcquires, c.acquireWait} {
		ch <- d
	}
}

// Collect sends the pool's statistics now.
func (c *PoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.pool.Stat()
	gauge := func(d *prometheus.Desc, v float64) { ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v) }
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}
	gauge(c.acquired, float64(s.AcquiredConns()))
	gauge(c.idle, float64(s.IdleConns()))
	gauge(c.total, float64(s.TotalConns()))
	gauge(c.maxConns, float64(s.MaxConns()))
	counter(c.acquires, float64(s.AcquireCount()))
	counter(c.emptyAcquires, float64(s.EmptyAcquireCount()))
	counter(c.acquireWait, s.AcquireDuration().Seconds())
}
