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
	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	"github.com/azverinus/flagnetic/internal/infra/config"
	flaghttp "github.com/azverinus/flagnetic/internal/infra/in/http"
	"github.com/azverinus/flagnetic/internal/infra/in/http/console"
	"github.com/azverinus/flagnetic/internal/infra/in/http/management"
	"github.com/azverinus/flagnetic/internal/infra/in/http/sdk"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/infra/out/password"
	"github.com/azverinus/flagnetic/internal/infra/out/postgres"
	pgaccess "github.com/azverinus/flagnetic/internal/infra/out/postgres/access"
	pgdelivery "github.com/azverinus/flagnetic/internal/infra/out/postgres/delivery"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
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
	// RulesetLoadFault lets a test stall the replica's ruleset
	// materializations; nil in production.
	RulesetLoadFault *pgdelivery.RulesetLoadFault
	// RateLimits are the limits of the SDK API.
	RateLimits config.RateLimits
	management.Options
}

// Background is the work a replica does besides answering requests, which
// whoever runs the replica starts.
type Background struct {
	// Workers are registered with the replica's runner.
	Workers []worker.Worker
	// Listeners are started on the replica's notification listeners.
	Listeners []Listener
	// Close ends the work the replica's use cases started on their own, such
	// as the ruleset loads in flight; whoever runs the replica calls it once
	// the requests are done.
	Close func()
}

// Listener is a handler of the notifications of one channel.
type Listener struct {
	Channel string
	Handler pglisten.Handler
}

// Management mounts the management API and the SDK API on edge, their
// metrics on reg, and returns the background work their use cases need.
func Management(edge *flaghttp.Server, d Deps, reg prometheus.Registerer, o Options) Background {
	tx, auditLog := postgres.NewTransactor(d.Pool), pggovernance.NewAuditLog(d.Pool)
	rec := appworkspace.NewRecorder(auditLog, d.Clock, d.IDs)
	accessStore, ws := pgaccess.NewStore(d.Pool), pgworkspace.NewStore(d.Pool)
	flagStore, flagRec := pgflags.NewStore(d.Pool), appflags.NewRecorder(auditLog, d.Clock, d.IDs)
	// Every write changing what SDKs receive moves the rulesets it changed
	// on, through the delivery context.
	deliveryStore := pgdelivery.NewStore(d.Pool)
	rulesets := appdelivery.NewBumpRulesets(deliveryStore, d.Clock)
	// Every replica serves rulesets from memory by version, learning each
	// version from its notification, and its own writes' versions as they
	// commit, before they are answered.
	rulesetCache := pgdelivery.NewRulesetCache(pgdelivery.RulesetCacheOptions{
		Load: appdelivery.NewMaterializeRuleset(appdelivery.MaterializeRulesetDeps{
			Snapshots: tx, Environments: deliveryStore, Declarations: ws, Flags: flagStore,
		}),
		Clock: d.Clock, Registerer: reg, Logger: edge.Logger(), Fault: o.RulesetLoadFault,
	})
	deliveryStore.OnCommitted(rulesetCache.Committed)
	sdkKeys := appdelivery.SDKKeyDeps{
		Tx: tx, Store: deliveryStore, Audit: auditLog, Clock: d.Clock, IDs: d.IDs, Random: rand.Reader,
	}
	configuration := appflags.ConfigurationDeps{Tx: tx, Projects: flagStore, Store: flagStore, Rulesets: rulesets, Recorder: flagRec}
	owners := appaccess.NewOwnership(accessStore)
	// Deleting a unit type or attribute asks the flags context what still
	// names it.
	declarationGuard := appflags.NewDeclarationGuard(flagStore)
	// The unit type and attribute lists count their usages through the
	// flags context too.
	declarationUsages := appflags.NewDeclarationUsages(flagStore)
	sessionKey := o.Keyring.Key(secret.UseSessionCookie)
	// Every request judging a session reads the settings: the replica keeps
	// a copy, which a notification of each change drops.
	settings := pgaccess.NewAuthSettingsCache(d.Pool)
	// One hasher serves every use case of the replica, so its bound is the
	// replica's.
	hasher := password.NewBoundedHasher(password.DefaultParams,
		semaphore.NewWeighted(int64(o.PasswordHashConcurrency)), password.SlotWait)
	// Token uses are gathered per replica and written in rounds, so no
	// request waits on recording one.
	tokenUses := appaccess.NewTokenUses(accessStore)
	management.Mount(edge, management.Handlers{
		AuthenticateToken:   appaccess.NewAuthenticateToken(accessStore, tokenUses, d.Clock),
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
		UpdateMe:           appaccess.NewUpdateMe(tx, accessStore, auditLog, d.Clock, d.IDs, config.ConsoleLocales),
		ChangeMyPassword: appaccess.NewChangeMyPassword(appaccess.ChangeMyPasswordDeps{
			Tx: tx, Store: accessStore, Settings: settings, Hasher: hasher, Audit: auditLog, Clock: d.Clock, IDs: d.IDs,
		}),
		DeactivateMember:  appaccess.NewDeactivateMember(tx, accessStore, auditLog, d.Clock, d.IDs),
		ReactivateMember:  appaccess.NewReactivateMember(tx, accessStore, auditLog, d.Clock, d.IDs, rand.Reader),
		InviteMember:      appaccess.NewInviteMember(tx, accessStore, auditLog, d.Clock, d.IDs, rand.Reader),
		ReissueInvitation: appaccess.NewReissueInvitation(tx, accessStore, auditLog, d.Clock, d.IDs, rand.Reader),
		GetInvitation:     appaccess.NewGetInvitation(accessStore, d.Clock),
		AcceptInvitation: appaccess.NewAcceptInvitation(appaccess.AcceptInvitationDeps{
			Tx: tx, Store: accessStore, Settings: settings, Hasher: hasher, Audit: auditLog, Clock: d.Clock, IDs: d.IDs,
			Random: rand.Reader, Key: sessionKey,
		}),
		ListPersonalTokens:  appaccess.NewListPersonalTokens(accessStore),
		CreatePersonalToken: appaccess.NewCreatePersonalToken(tx, accessStore, settings, auditLog, d.Clock, d.IDs, rand.Reader),
		RevokePersonalToken: appaccess.NewRevokePersonalToken(tx, accessStore, auditLog, d.Clock, d.IDs),
		ListProjectMembers:  appaccess.NewListProjectMembers(accessStore),
		CountProjectMembers: appaccess.NewCountProjectMembers(accessStore),
		ListGrants:          appaccess.NewListGrants(accessStore),
		CreateGrant:         appaccess.NewCreateGrant(tx, accessStore, auditLog, d.Clock, d.IDs),
		DeleteGrant:         appaccess.NewDeleteGrant(tx, accessStore, auditLog, d.Clock, d.IDs),
		ListProjectTokens:   appaccess.NewListProjectTokens(accessStore),
		CreateProjectToken:  appaccess.NewCreateProjectToken(tx, accessStore, settings, auditLog, d.Clock, d.IDs, rand.Reader),
		RevokeProjectToken:  appaccess.NewRevokeProjectToken(tx, accessStore, auditLog, d.Clock, d.IDs),
		ProjectPermissions:  appaccess.NewProjectPermissions(accessStore),
		WorkingProjects:     appaccess.NewWorkingProjects(accessStore),

		FindProject:   appworkspace.NewFindProject(ws),
		ListProjects:  appworkspace.NewListProjects(ws),
		CreateProject: appworkspace.NewCreateProject(tx, ws, rec),
		UpdateProject: appworkspace.NewUpdateProject(tx, ws, rec),

		ListEnvironments:  appworkspace.NewListEnvironments(ws),
		GetEnvironment:    appworkspace.NewGetEnvironment(ws),
		CreateEnvironment: appworkspace.NewCreateEnvironment(tx, ws, appflags.NewConfigureEnvironment(flagStore), rec),
		UpdateEnvironment: appworkspace.NewUpdateEnvironment(tx, ws, rec),
		DeleteEnvironment: appworkspace.NewDeleteEnvironment(tx, ws, rec),

		ListUnitTypes:  appworkspace.NewListUnitTypes(tx, ws, declarationUsages),
		GetUnitType:    appworkspace.NewGetUnitType(ws),
		CreateUnitType: appworkspace.NewCreateUnitType(tx, ws, rulesets, rec),
		UpdateUnitType: appworkspace.NewUpdateUnitType(tx, ws, rec),
		DeleteUnitType: appworkspace.NewDeleteUnitType(tx, ws, declarationGuard, rulesets, rec),

		ListAttributes:  appworkspace.NewListAttributes(tx, ws, declarationUsages),
		GetAttribute:    appworkspace.NewGetAttribute(ws),
		CreateAttribute: appworkspace.NewCreateAttribute(tx, ws, rulesets, rec),
		UpdateAttribute: appworkspace.NewUpdateAttribute(tx, ws, rec),
		DeleteAttribute: appworkspace.NewDeleteAttribute(tx, ws, declarationGuard, rulesets, rec),

		ListFlags: appflags.NewListFlags(tx, ws, flagStore),
		CreateFlag: appflags.NewCreateFlag(appflags.CreateFlagDeps{
			Tx: tx, Projects: flagStore, Environments: ws, Store: flagStore, Owners: owners, Rulesets: rulesets, Recorder: flagRec,
		}),
		GetFlag: appflags.NewGetFlag(tx, flagStore),
		UpdateFlag: appflags.NewUpdateFlag(appflags.UpdateFlagDeps{
			Tx: tx, Projects: flagStore, Store: flagStore, Owners: owners, Rulesets: rulesets, Recorder: flagRec,
		}),

		ArchiveFlag: appflags.NewArchiveFlag(appflags.ArchiveFlagDeps{
			Tx: tx, Projects: flagStore, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
		RestoreFlag: appflags.NewRestoreFlag(appflags.ArchivalDeps{
			Tx: tx, Projects: flagStore, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
		DeleteFlag: appflags.NewDeleteFlag(appflags.DeleteFlagDeps{
			Tx: tx, Projects: flagStore, Store: flagStore, Recorder: flagRec,
		}),

		GetFlagConfiguration: appflags.NewGetFlagConfiguration(tx, flagStore),
		ReplaceConfiguration: appflags.NewReplaceConfiguration(appflags.ReplaceConfigurationDeps{
			Tx: tx, Projects: flagStore, Declarations: ws, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
		KillFlag: appflags.NewKillFlag(configuration),
		SetOverride: appflags.NewSetOverride(appflags.SetOverrideDeps{
			Tx: tx, Projects: flagStore, Declarations: ws, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
		RemoveOverride: appflags.NewRemoveOverride(configuration),
		RegenerateSalt: appflags.NewRegenerateSalt(configuration),
		// A revert finds the version it writes again in the audit log.
		RevertConfiguration: appflags.NewRevertConfiguration(appflags.RevertConfigurationDeps{
			Tx: tx, Projects: flagStore, Declarations: ws, Store: flagStore, History: auditLog, Rulesets: rulesets, Recorder: flagRec,
		}),
		CopyConfiguration: appflags.NewCopyConfiguration(appflags.ReplaceConfigurationDeps{
			Tx: tx, Projects: flagStore, Declarations: ws, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
		// The tester evaluates through the delivery context, which asks
		// the flags context to check a draft as a write would.
		Evaluate: appdelivery.NewEvaluate(appdelivery.EvaluateDeps{
			Snapshots: tx, Environments: deliveryStore, Declarations: ws, Flags: flagStore,
			Drafts: appflags.NewDraftConfiguration(ws, flagStore, d.IDs),
		}),
		ReadRuleset: appdelivery.NewReadRuleset(deliveryStore, rulesetCache.Unmeasured()),

		ListSDKKeys:  appdelivery.NewListSDKKeys(deliveryStore, d.Clock),
		CreateSDKKey: appdelivery.NewCreateSDKKey(sdkKeys),
		UpdateSDKKey: appdelivery.NewUpdateSDKKey(sdkKeys),
		RotateSDKKey: appdelivery.NewRotateSDKKey(sdkKeys),
		RevokeSDKKey: appdelivery.NewRevokeSDKKey(sdkKeys),

		ListSegments: appflags.NewListSegments(tx, flagStore),
		GetSegment:   appflags.NewGetSegment(flagStore),
		CreateSegment: appflags.NewCreateSegment(appflags.CreateSegmentDeps{
			Tx: tx, Projects: flagStore, Declarations: ws, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
		UpdateSegment: appflags.NewUpdateSegment(appflags.UpdateSegmentDeps{
			Tx: tx, Projects: flagStore, Declarations: ws, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
		DeleteSegment: appflags.NewDeleteSegment(appflags.DeleteSegmentDeps{
			Tx: tx, Projects: flagStore, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
		ListSegmentUsages: appflags.NewListSegmentUsages(tx, flagStore),

		ListAuditEntries: appgovernance.NewListAuditEntries(auditLog),
		GetAuditEntry:    appgovernance.NewGetAuditEntry(auditLog),
	}, reg, o.Options)
	// Every replica keeps the SDK keys it authenticated, dropped on a
	// notification of each change, and writes their uses in rounds.
	sdkKeyCache := pgdelivery.NewSDKKeyCache(deliveryStore)
	sdkKeyUses := appdelivery.NewSDKKeyUses(deliveryStore)
	// Every replica offers each new ruleset version to the streams it holds
	// once it has loaded it, woken by the notification of each bump.
	rulesetStream := pgdelivery.NewRulesetStream(rulesetCache, edge.Logger())
	sdk.Mount(edge, sdk.Handlers{
		Authenticate: appdelivery.NewAuthenticateSDKKey(sdkKeyCache, sdkKeyUses, d.Clock),
		ServeRuleset: appdelivery.NewServeRuleset(rulesetCache),
		WatchRuleset: appdelivery.NewWatchRuleset(rulesetStream, rulesetCache.Unmeasured()),
	}, sdk.Options{Clock: d.Clock, RateLimits: o.RateLimits, Registerer: reg})
	return Background{
		Workers: []worker.Worker{
			worker.AuthRetention(appaccess.NewPurgeAuthRecords(tx, accessStore, settings, d.Clock), reg),
			worker.TokenLastUse(tokenUses),
			worker.SDKKeyLastUse(sdkKeyUses),
		},
		Listeners: []Listener{
			{Channel: pgaccess.AuthSettingsChannel, Handler: settings},
			{Channel: pgdelivery.SDKKeysChannel, Handler: sdkKeyCache},
			{Channel: pgdelivery.RulesetsChannel, Handler: rulesetStream},
		},
		Close: func() {
			rulesetStream.Close()
			rulesetCache.Close()
		},
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
