// Package compose assembles a replica: it builds the application's handlers
// on their PostgreSQL and other outbound adapters and mounts the APIs on the
// edge. The serve command and the test kit's in-process server both use it,
// so tests exercise the replica that runs.
package compose

import (
	"crypto/rand"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/out/password"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Deps are what a replica's use cases run on.
type Deps struct {
	Pool  *pgxpool.Pool
	Clock clock.Clock
	IDs   *id.Source
}

// NewDeps returns the dependencies of a replica on pool, timed by c, minting
// identifiers from c.
func NewDeps(pool *pgxpool.Pool, c clock.Clock) Deps {
	return Deps{Pool: pool, Clock: c, IDs: id.NewSource(c)}
}

// Management mounts the management API on edge, its metrics on reg.
func Management(edge *flaghttp.Server, d Deps, reg prometheus.Registerer) {
	store := pgaccess.NewStore(d.Pool)
	management.Mount(edge, management.Handlers{
		AuthenticateToken: appaccess.NewAuthenticateToken(store, d.Clock),
	}, reg)
}

// Bootstrap returns the use case creating the first instance admin.
func Bootstrap(d Deps) *appaccess.Bootstrap {
	return appaccess.NewBootstrap(postgres.NewTransactor(d.Pool), pgaccess.NewStore(d.Pool),
		password.NewHasher(password.DefaultParams), pggovernance.NewAuditLog(d.Pool),
		d.Clock, d.IDs, rand.Reader)
}

// InstanceAdminExists returns the query telling whether the instance has an
// instance admin.
func InstanceAdminExists(d Deps) *appaccess.InstanceAdminExists {
	return appaccess.NewInstanceAdminExists(pgaccess.NewStore(d.Pool))
}
