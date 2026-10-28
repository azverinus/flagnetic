package testkit

import (
	"bufio"
	"cmp"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/infra/compose"
	"github.com/azverinus/flagnetic/internal/infra/config"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/infra/metrics"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	"github.com/azverinus/flagnetic/internal/pkg/pglisten"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/secret"
)

// Server is a replica's public handler served in-process, for black-box HTTP
// tests without a network listener of the binary's own.
type Server struct {
	// URL is the base URL, such as http://127.0.0.1:53211.
	URL string
	// Logs captures everything the server logs.
	Logs *LogSink

	edge       *flaghttp.Server
	metrics    http.Handler
	runner     *worker.Runner
	listeners  *postgres.Listeners
	background func()
	pool       *pgxpool.Pool
	hold       *pgevents.IngestHold
}

// CloseBackground ends the work the replica's use cases started on their
// own, such as ruleset loads nobody waits for, as stopping the replica
// does.
func (s *Server) CloseBackground() { s.background() }

// Drain begins the replica's drain, as the stop signal does.
func (s *Server) Drain() { s.edge.Drain() }

// Listen starts a notification listener of the server's replica, reported on
// its registry, until the test ends.
func (s *Server) Listen(channel string, h pglisten.Handler, opts pglisten.Options) {
	s.listeners.Start(channel, h, opts)
}

// Metric returns the value of one sample of a scrape, named as it is written,
// such as `flagnetic_listener_up{channel="rulesets"}`, or -1 when the scrape
// has none.
func Metric(t testing.TB, scrape, series string) float64 {
	t.Helper()
	sc := bufio.NewScanner(strings.NewReader(scrape))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), series+" "); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatalf("%s: %v", series, err)
			}
			return f
		}
	}
	return -1
}

// AwaitListening returns once the replica listens on channel, failing after
// 5 seconds. A replica starts listening shortly after it starts serving; a
// test that must not see what a replica does while it is not listening yet
// waits for it.
func (s *Server) AwaitListening(t testing.TB, channel string) {
	t.Helper()
	series := `flagnetic_listener_up{channel="` + channel + `"}`
	for deadline := time.Now().Add(5 * time.Second); Metric(t, s.Scrape(t), series) != 1; {
		if time.Now().After(deadline) {
			t.Fatalf("the replica is not listening on %s after 5 seconds", channel)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Tick runs one round of the worker called name, through the runner's
// wrapper. The in-process server never starts the workers' timers: a test
// drives every round it wants, when it wants.
func (s *Server) Tick(t testing.TB, name string) error {
	t.Helper()
	return s.runner.Tick(t.Context(), name)
}

// Scrape returns the server's metrics in the Prometheus text format, as the
// metrics listener serves them.
func (s *Server) Scrape(t testing.TB) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.metrics.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("scrape = %d: %s", rec.Code, rec.Body)
	}
	return rec.Body.String()
}

type serverOptions struct {
	web     management.Options
	clock   clock.Clock
	workers []worker.Worker
	pool    *pgxpool.Pool
	edge    []func(*flaghttp.Options)
	routes  []route
	// console is the default locale of the console served, "" for none.
	console string
	listen  pglisten.Options
	targets governance.TargetPolicy
	fault   *pgdelivery.RulesetLoadFault
	limits  []func(*config.RateLimits)
	// auditExportMax is the most entries a download of the audit log holds,
	// 0 for the configuration's default.
	auditExportMax int
	// digest is when the weekly stale-flag digest is due, the
	// configuration's default when nil.
	digest *governance.DigestSlot
}

type route struct {
	pattern string
	build   func(*flaghttp.Server) http.Handler
	opts    []flaghttp.RouteOption
}

// WithRoute mounts the handler build returns on pattern, for a test to
// exercise the edge through routes of its own. build receives the server, to
// answer errors through it or log with its logger.
func WithRoute(pattern string, build func(*flaghttp.Server) http.Handler, opts ...flaghttp.RouteOption) ServerOption {
	return func(o *serverOptions) { o.routes = append(o.routes, route{pattern, build, opts}) }
}

// WithWorker registers w with the server's runner, for Tick.
func WithWorker(w worker.Worker) ServerOption {
	return func(o *serverOptions) { o.workers = append(o.workers, w) }
}

// WithEdge changes the edge's options, which otherwise are the defaults of
// the configuration.
func WithEdge(change func(*flaghttp.Options)) ServerOption {
	return func(o *serverOptions) { o.edge = append(o.edge, change) }
}

// ServerOption changes what the in-process server is built with.
type ServerOption func(*serverOptions)

// WithListenerOptions starts the replica's notification listeners with o,
// such as a short ping interval and the fault hook that silences them.
func WithListenerOptions(o pglisten.Options) ServerOption {
	return func(so *serverOptions) { so.listen = o }
}

// SilenceableListener returns listener options whose fault lets a test
// silence the listener and have it found deaf within about a second. Pings
// go out every 50ms so the silence is noticed soon after it starts, but each
// may take a full second to come back: under the race detector on a busy CI
// runner a round trip to PostgreSQL can exceed a tighter timeout, and a
// listener found deaf by a slow pong before the test silenced it waits out
// its backoff instead of being up when the test expects it to be. Once
// found deaf while silenced, the listener stays down until the fault is
// restored, so the default backoff serves.
func SilenceableListener(f *pglisten.Fault) pglisten.Options {
	return pglisten.Options{PingInterval: 50 * time.Millisecond, PingTimeout: time.Second, Fault: f}
}

// WithRulesetLoadFault lets f stall the replica's ruleset materializations.
func WithRulesetLoadFault(f *pgdelivery.RulesetLoadFault) ServerOption {
	return func(o *serverOptions) { o.fault = f }
}

// WithRateLimits changes the SDK API's limits, which otherwise are the
// defaults of the configuration.
func WithRateLimits(change func(*config.RateLimits)) ServerOption {
	return func(o *serverOptions) { o.limits = append(o.limits, change) }
}

// WithAuditExportMaxEntries serves with n as the most entries one download of
// the audit log may hold, instead of the configuration's default.
func WithAuditExportMaxEntries(n int) ServerOption {
	return func(o *serverOptions) { o.auditExportMax = n }
}

// WithDigestSlot serves with the weekly stale-flag digest due at slot instead
// of the configuration's default.
func WithDigestSlot(slot governance.DigestSlot) ServerOption {
	return func(o *serverOptions) { o.digest = &slot }
}

// WithWebhookTargets serves allowing webhook targets as p does, such as a
// receiver the test runs on loopback over plain http.
func WithWebhookTargets(p governance.TargetPolicy) ServerOption {
	return func(o *serverOptions) { o.targets = p }
}

// WithClock serves with c as the application clock instead of the wall clock.
func WithClock(c clock.Clock) ServerOption {
	return func(o *serverOptions) { o.clock = c }
}

// WithBaseURL serves as if the console's public URL were baseURL, such as
// https://flags.example.com.
func WithBaseURL(baseURL string) ServerOption {
	return func(o *serverOptions) {
		u, err := url.Parse(baseURL)
		if err != nil {
			panic(err)
		}
		o.web.BaseURL = u
	}
}

// WithInsecureCookies serves as FLAGNETIC_INSECURE_COOKIES does.
func WithInsecureCookies() ServerOption {
	return func(o *serverOptions) { o.web.InsecureCookies = true }
}

// WithConsole serves the embedded console too, as serve does, its entry
// document naming defaultLocale.
func WithConsole(defaultLocale string) ServerOption {
	return func(o *serverOptions) { o.console = defaultLocale }
}

// defaultHashConcurrency is FLAGNETIC_PASSWORD_HASH_CONCURRENCY's default.
const defaultHashConcurrency = 4

// SecretKey is the secret key of every in-process server.
const SecretKey = "test secret key of the in-process server, 32+ bytes"

// WithPool serves with pool instead of the package's migrated one, such as a
// pool whose PostgreSQL is unreachable.
func WithPool(pool *pgxpool.Pool) ServerOption {
	return func(o *serverOptions) { o.pool = pool }
}

// StartServer serves the public handler until the test ends.
func StartServer(t testing.TB, opts ...ServerOption) *Server {
	t.Helper()
	o := serverOptions{}
	for _, opt := range opts {
		opt(&o)
	}
	if o.pool == nil {
		o.pool = Pool(t)
	}
	logs := NewLogSink()
	cfg := defaults(t)
	edge := flaghttp.OptionsFrom(cfg)
	limits := cfg.RateLimits
	for _, change := range o.limits {
		change(&limits)
	}
	reg := metrics.NewRegistry()
	reg.MustRegister(postgres.NewPoolCollector(o.pool))
	edge.Logger, edge.DB, edge.Metrics = logs.Logger(), o.pool, flaghttp.NewMetrics(reg)
	for _, change := range o.edge {
		change(&edge)
	}
	runner := worker.NewRunner(logs.Logger(), reg)
	for _, w := range o.workers {
		if err := runner.Register(w); err != nil {
			t.Fatal(err)
		}
	}
	h := flaghttp.New(edge)
	digest := governance.DigestSlot{Weekday: cfg.DigestWeekday, Hour: cfg.DigestHour}
	if o.digest != nil {
		digest = *o.digest
	}
	c := o.clock
	if c == nil {
		c = clock.System{}
	}
	hold := pgevents.NewIngestHold()
	background := compose.Management(h, compose.NewDeps(o.pool, c), reg, compose.Options{
		Keyring: secret.NewKeyring(secret.New(SecretKey)), PasswordHashConcurrency: defaultHashConcurrency, Options: o.web,
		RulesetLoadFault: o.fault, IngestHold: hold, RateLimits: limits,
		EventsRetention:       time.Duration(cfg.EventsRetentionDays) * 24 * time.Hour,
		EventsPartitionsAhead: cfg.EventsPartitionsAhead,
		SDKInventoryRetention: time.Duration(cfg.SDKInventoryRetentionDays) * 24 * time.Hour,
		WebhookTargets:        o.targets,
		WebhookTick:           cfg.WebhookTick,
		WebhookSecretOverlap:  cfg.WebhookSecretOverlap,
		AuditExportMaxEntries: cmp.Or(o.auditExportMax, cfg.AuditExportMaxEntries),
		Schedule: compose.Schedule{
			MinLead: cfg.ScheduleMinLead, Tick: cfg.ScheduleTick, MaxFailures: cfg.ScheduleMaxFailures,
		},
		DigestSlot: digest,
	})
	// Registered first, so it runs once the server below has stopped.
	t.Cleanup(background.Close)
	for _, w := range background.Workers {
		if err := runner.Register(w); err != nil {
			t.Fatal(err)
		}
	}
	if o.console != "" {
		if _, err := compose.Console(h, o.console); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range o.routes {
		h.Handle(r.pattern, r.build(h), r.opts...)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Config = h.HTTPServer()
	srv.Start()
	t.Cleanup(srv.Close)
	listeners := postgres.NewListeners(runner.PanicHook(context.Background()), o.pool, logs.Logger(), reg)
	t.Cleanup(func() { listeners.Stop(10 * time.Second) })
	for _, l := range background.Listeners {
		listeners.Start(l.Channel, l.Handler, o.listen)
	}
	return &Server{
		URL: srv.URL, Logs: logs, edge: h, metrics: metrics.Handler(reg), runner: runner, listeners: listeners,
		background: background.Close, pool: o.pool, hold: hold,
	}
}

// defaults returns the configuration a replica has when nothing is set.
func defaults(t testing.TB) config.Config {
	t.Helper()
	cfg, err := config.Load(func(k string) (string, bool) {
		if k == "DATABASE_URL" {
			return "postgres://localhost/unused", true
		}
		return "", false
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// RestartListeners ends the replica's notification listeners and starts them
// again on new connections, as a database failover or restore makes them do.
func (s *Server) RestartListeners(t testing.TB) {
	t.Helper()
	if !s.listeners.Restart(10 * time.Second) {
		t.Fatal("the listeners did not stop in time")
	}
}
