// Package compose assembles a replica: it builds the application's handlers
// on their PostgreSQL and other outbound adapters and mounts the APIs on the
// edge. The serve command and the test kit's in-process server both use it,
// so tests exercise the replica that runs.
package compose

import (
	"crypto/rand"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/semaphore"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/console"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/infra/out/password"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/pkg/pglisten"
	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/shared/secret"
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

// Options are how a replica serves, as its configuration sets them.
type Options struct {
	// Keyring holds the instance's secret key, which session cookies are
	// signed under.
	Keyring *secret.Keyring
	// PasswordHashConcurrency bounds the password hashes and verifications
	// the replica runs at once.
	PasswordHashConcurrency int
	management.Options
}

// Background is the work a replica does besides answering requests, which
// whoever runs the replica starts.
type Background struct {
	// Workers are registered with the replica's runner.
	Workers []worker.Worker
	// Listeners are started on the replica's notification listeners.
	Listeners []Listener
}

// Listener is a handler of the notifications of one channel.
type Listener struct {
	Channel string
	Handler pglisten.Handler
}

// Management mounts the management API on edge, its metrics on reg, and
// returns the background work its use cases need.
func Management(edge *flaghttp.Server, d Deps, reg prometheus.Registerer, o Options) Background {
	tx, auditLog := postgres.NewTransactor(d.Pool), pggovernance.NewAuditLog(d.Pool)
	rec := appworkspace.NewRecorder(auditLog, d.Clock, d.IDs)
	accessStore, ws := pgaccess.NewStore(d.Pool), pgworkspace.NewStore(d.Pool)
	sessionKey := o.Keyring.Key(secret.UseSessionCookie)
	// Every request judging a session reads the settings: the replica keeps
	// a copy, which a notification of each change drops.
	settings := pgaccess.NewAuthSettingsCache(d.Pool)
	// One hasher serves every use case of the replica, so its bound is the
	// replica's.
	hasher := password.NewBoundedHasher(password.DefaultParams,
		semaphore.NewWeighted(int64(o.PasswordHashConcurrency)), password.SlotWait)
	management.Mount(edge, management.Handlers{
		AuthenticateToken:   appaccess.NewAuthenticateToken(accessStore, d.Clock),
		AuthenticateSession: appaccess.NewAuthenticateSession(accessStore, settings, d.Clock, sessionKey),
		SignIn: appaccess.NewSignIn(appaccess.SignInDeps{
			Tx: tx, Store: accessStore, Settings: settings, Hasher: hasher, Audit: auditLog, Clock: d.Clock, IDs: d.IDs,
			Random: rand.Reader, Key: sessionKey,
		}),
		SignOut:            appaccess.NewSignOut(tx, accessStore, auditLog, d.Clock, d.IDs),
		ListSessions:       appaccess.NewListSessions(accessStore, settings, d.Clock),
		RevokeSessions:     appaccess.NewRevokeSessions(tx, accessStore, settings, auditLog, d.Clock, d.IDs),
		GetAuthSettings:    appaccess.NewGetAuthSettings(settings),
		UpdateAuthSettings: appaccess.NewUpdateAuthSettings(tx, accessStore, settings, auditLog, d.Clock, d.IDs),
		ListMembers:        appaccess.NewListMembers(accessStore),
		GetMember:          appaccess.NewGetMember(accessStore),
		UpdateMember:       appaccess.NewUpdateMember(tx, accessStore, auditLog, d.Clock, d.IDs),
		DeactivateMember:   appaccess.NewDeactivateMember(tx, accessStore, auditLog, d.Clock, d.IDs),
		ReactivateMember:   appaccess.NewReactivateMember(tx, accessStore, auditLog, d.Clock, d.IDs, rand.Reader),
		InviteMember:       appaccess.NewInviteMember(tx, accessStore, auditLog, d.Clock, d.IDs, rand.Reader),
		ReissueInvitation:  appaccess.NewReissueInvitation(tx, accessStore, auditLog, d.Clock, d.IDs, rand.Reader),
		GetInvitation:      appaccess.NewGetInvitation(accessStore, d.Clock),
		AcceptInvitation: appaccess.NewAcceptInvitation(appaccess.AcceptInvitationDeps{
			Tx: tx, Store: accessStore, Settings: settings, Hasher: hasher, Audit: auditLog, Clock: d.Clock, IDs: d.IDs,
			Random: rand.Reader, Key: sessionKey,
		}),
		ProjectPermissions: appaccess.NewProjectPermissions(),
		WorkingProjects:    appaccess.NewWorkingProjects(),

		FindProject:   appworkspace.NewFindProject(ws),
		ListProjects:  appworkspace.NewListProjects(ws),
		CreateProject: appworkspace.NewCreateProject(tx, ws, rec),
		UpdateProject: appworkspace.NewUpdateProject(tx, ws, rec),

		ListEnvironments:  appworkspace.NewListEnvironments(ws),
		GetEnvironment:    appworkspace.NewGetEnvironment(ws),
		CreateEnvironment: appworkspace.NewCreateEnvironment(tx, ws, rec),
		UpdateEnvironment: appworkspace.NewUpdateEnvironment(tx, ws, rec),
		DeleteEnvironment: appworkspace.NewDeleteEnvironment(tx, ws, rec),

		ListUnitTypes:  appworkspace.NewListUnitTypes(ws),
		GetUnitType:    appworkspace.NewGetUnitType(ws),
		CreateUnitType: appworkspace.NewCreateUnitType(tx, ws, rec),
		UpdateUnitType: appworkspace.NewUpdateUnitType(tx, ws, rec),
		DeleteUnitType: appworkspace.NewDeleteUnitType(tx, ws, rec),

		ListAttributes:  appworkspace.NewListAttributes(ws),
		GetAttribute:    appworkspace.NewGetAttribute(ws),
		CreateAttribute: appworkspace.NewCreateAttribute(tx, ws, rec),
		UpdateAttribute: appworkspace.NewUpdateAttribute(tx, ws, rec),
		DeleteAttribute: appworkspace.NewDeleteAttribute(tx, ws, rec),

		ListAuditEntries: appgovernance.NewListAuditEntries(auditLog),
		GetAuditEntry:    appgovernance.NewGetAuditEntry(auditLog),
	}, reg, o.Options)
	return Background{
		Workers:   []worker.Worker{worker.AuthRetention(appaccess.NewPurgeAuthRecords(tx, accessStore, settings, d.Clock), reg)},
		Listeners: []Listener{{Channel: pgaccess.AuthSettingsChannel, Handler: settings}},
	}
}

// Hasher returns the password hasher of a command line, which hashes one
// password and needs no bound.
func Hasher() *password.Hasher { return password.NewHasher(password.DefaultParams) }

// Bootstrap returns the use case creating the first instance admin.
func Bootstrap(d Deps) *appaccess.Bootstrap {
	return appaccess.NewBootstrap(postgres.NewTransactor(d.Pool), pgaccess.NewStore(d.Pool),
		Hasher(), pggovernance.NewAuditLog(d.Pool),
		d.Clock, d.IDs, rand.Reader)
}

// CreateInstanceAdmin returns the use case adding an instance admin from the
// command line.
func CreateInstanceAdmin(d Deps) *appaccess.CreateInstanceAdmin {
	return appaccess.NewCreateInstanceAdmin(postgres.NewTransactor(d.Pool), pgaccess.NewStore(d.Pool), Hasher(),
		pggovernance.NewAuditLog(d.Pool), d.Clock, d.IDs, rand.Reader)
}

// PromoteMember returns the use case making a member an instance admin from
// the command line.
func PromoteMember(d Deps) *appaccess.PromoteMember {
	return appaccess.NewPromoteMember(postgres.NewTransactor(d.Pool), pgaccess.NewStore(d.Pool),
		pggovernance.NewAuditLog(d.Pool), d.Clock, d.IDs)
}

// InstanceAdminExists returns the query telling whether the instance has an
// instance admin.
func InstanceAdminExists(d Deps) *appaccess.InstanceAdminExists {
	return appaccess.NewInstanceAdminExists(pgaccess.NewStore(d.Pool))
}

// Console mounts the embedded console on edge, its entry document naming
// defaultLocale, and reports whether the binary holds a build of the console
// rather than the placeholder page.
func Console(edge *flaghttp.Server, defaultLocale string) (bool, error) {
	fsys, built := console.FS()
	h, err := console.Handler(fsys, defaultLocale)
	if err != nil {
		return false, err
	}
	edge.Console(h)
	return built, nil
}
