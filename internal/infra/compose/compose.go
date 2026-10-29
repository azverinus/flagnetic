// Package compose assembles a replica: it builds the application's handlers
// on their PostgreSQL and other outbound adapters and mounts the APIs on the
// edge. The serve command and the test kit's in-process server both use it,
// so tests exercise the replica that runs.
package compose

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/semaphore"

	appaccess "github.com/azverinus/flagnetic/internal/app/access"
	appdelivery "github.com/azverinus/flagnetic/internal/app/delivery"
	appevents "github.com/azverinus/flagnetic/internal/app/events"
	appexperiments "github.com/azverinus/flagnetic/internal/app/experiments"
	appflags "github.com/azverinus/flagnetic/internal/app/flags"
	appgovernance "github.com/azverinus/flagnetic/internal/app/governance"
	appsinks "github.com/azverinus/flagnetic/internal/app/sinks"
	appworkspace "github.com/azverinus/flagnetic/internal/app/workspace"
	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/governance"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
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
	pgevents "github.com/azverinus/flagnetic/internal/infra/out/postgres/events"
	pgexperiments "github.com/azverinus/flagnetic/internal/infra/out/postgres/experiments"
	pgflags "github.com/azverinus/flagnetic/internal/infra/out/postgres/flags"
	pggovernance "github.com/azverinus/flagnetic/internal/infra/out/postgres/governance"
	pgsinks "github.com/azverinus/flagnetic/internal/infra/out/postgres/sinks"
	pgworkspace "github.com/azverinus/flagnetic/internal/infra/out/postgres/workspace"
	"github.com/azverinus/flagnetic/internal/infra/out/webhook"
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
	// IngestHold lets a test keep a batch's transaction open; nil in
	// production.
	IngestHold *pgevents.IngestHold
	// RateLimits are the limits of the SDK API.
	RateLimits config.RateLimits
	// EventsRetention is how long raw events are kept.
	EventsRetention time.Duration
	// EventsPartitionsAhead is how many days after today the raw event
	// tables' partitions are created ahead of time.
	EventsPartitionsAhead int
	// SDKInventoryRetention is how long the SDK inventory keeps an SDK
	// after it was last seen.
	SDKInventoryRetention time.Duration
	// WebhookTargets is what the operator allows a webhook subscription's
	// target to be.
	WebhookTargets governance.TargetPolicy
	// WebhookTick is how often the replica fans the outbox out and sends the
	// webhook deliveries that are due.
	WebhookTick time.Duration
	// WebhookSecretOverlap is how long a webhook subscription's rotated
	// secret signs beside the new one.
	WebhookSecretOverlap time.Duration
	// Schedule is how change requests are scheduled and applied at their
	// time.
	Schedule Schedule
	// AuditExportMaxEntries is the most entries one download of the audit
	// log may hold.
	AuditExportMaxEntries int
	// DigestSlot is when the weekly stale-flag digest is sent.
	DigestSlot governance.DigestSlot
	management.Options
}

// Schedule is how change requests are scheduled: how far ahead an apply
// time is at least, how often the scheduler runs, and after how many
// failures in a row it stops trying a change request.
type Schedule struct {
	MinLead     time.Duration
	Tick        time.Duration
	MaxFailures int
}

// protectionGuardConns is the size of the protection guard's own pool: a
// read of what an environment served before a write is needed only when the
// replica's memory does not hold it, and writes to one project wait for each
// other anyway.
const protectionGuardConns = 2

// finalFlushBound bounds the writes a stopping replica makes of what it
// gathered since its last round, so a database that does not answer never
// holds the stop.
const finalFlushBound = 5 * time.Second

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
	tx, auditLog, outbox := postgres.NewTransactor(d.Pool), pggovernance.NewAuditLog(d.Pool), pggovernance.NewOutbox(d.Pool)
	// Every context records its audit entries through the governance
	// context, which supersedes the change requests they move, writes the
	// catalogue events they cause to the outbox and announces them to the
	// management change streams.
	changeRequestStore := pggovernance.NewChangeRequests(d.Pool)
	audit := appgovernance.NewRecord(auditLog, changeRequestStore, appflags.NewSegmentReferrers(pgflags.NewStore(d.Pool)), d.IDs,
		appgovernance.WithOutbox(outbox, d.IDs), appgovernance.WithChangeStream(auditLog))
	// Every replica offers the entries announced to the change streams it
	// holds, reading them from the audit log.
	changeHub := pggovernance.NewChangeHub(auditLog, edge.Logger())
	rec := appworkspace.NewRecorder(audit, d.Clock, d.IDs)
	accessStore, ws := pgaccess.NewStore(d.Pool), pgworkspace.NewStore(d.Pool)
	flagStore, flagRec := pgflags.NewStore(d.Pool), appflags.NewRecorder(audit, d.Clock, d.IDs)
	// Every write changing what SDKs receive moves the rulesets it changed
	// on, through the delivery context.
	deliveryStore := pgdelivery.NewStore(d.Pool)
	eventStore := pgevents.NewStore(d.Pool)
	materialize := appdelivery.NewMaterializeRuleset(appdelivery.MaterializeRulesetDeps{
		Snapshots: tx, Environments: deliveryStore, Declarations: ws, Flags: flagStore,
	})
	// Every replica serves rulesets from memory by version, learning each
	// version from its notification, and its own writes' versions as they
	// commit, before they are answered.
	// The protection guard reads what a protected environment serves before
	// a write while the write, and the writes queued behind its project's
	// row, hold connections of the pool: it reads on two connections of its
	// own, which no write holds.
	guardPool, err := postgres.SidePool(d.Pool, protectionGuardConns)
	if err != nil {
		panic(fmt.Sprintf("compose: the protection guard's pool: %v", err)) // only an invalid configuration fails, which Pool already accepted
	}
	committedLoad := appdelivery.NewMaterializeRuleset(appdelivery.MaterializeRulesetDeps{
		Snapshots: postgres.NewTransactor(guardPool), Environments: pgdelivery.NewStore(guardPool),
		Declarations: pgworkspace.NewStore(guardPool), Flags: pgflags.NewStore(guardPool),
	})
	rulesetCache := pgdelivery.NewRulesetCache(pgdelivery.RulesetCacheOptions{
		Load: materialize, CommittedLoad: committedLoad,
		Clock: d.Clock, Registerer: reg, Logger: edge.Logger(), Fault: o.RulesetLoadFault,
	})
	deliveryStore.OnCommitted(rulesetCache.Committed)
	// The bump refuses a write that would change a protected environment's
	// ruleset, comparing what the write would serve with what the cache
	// serves now.
	rulesets := pggovernance.NewProtectionGuard(pggovernance.ProtectionGuardDeps{
		Pool: d.Pool, Next: appdelivery.NewBumpRulesets(deliveryStore, d.Clock),
		After: materialize, Before: rulesetCache, Registerer: reg,
	})
	// A subscription's secret is sealed under the instance's secret key, and
	// its target's name resolved by the system's resolver.
	webhookStore := pggovernance.NewWebhooks(d.Pool)
	webhooks := appgovernance.WebhookDeps{
		Tx: tx, Store: webhookStore, Audit: audit, Clock: d.Clock, IDs: d.IDs, Random: rand.Reader,
		Sealer: o.Keyring, Resolver: net.DefaultResolver, Targets: o.WebhookTargets, SecretOverlap: o.WebhookSecretOverlap,
	}
	// A sink's secret is sealed under the same key, its target resolved by the
	// same resolver and judged by the webhook subscriptions' policy.
	sinks := appsinks.Deps{
		Tx: tx, Store: pgsinks.NewStore(d.Pool), Audit: audit, Clock: d.Clock, IDs: d.IDs, Random: rand.Reader,
		Sealer: o.Keyring, Resolver: net.DefaultResolver, Targets: o.WebhookTargets, SecretOverlap: o.WebhookSecretOverlap,
	}
	deliveryRequests := appgovernance.DeliveryRequestDeps{
		Tx: tx, Webhooks: webhookStore, Deliveries: webhookStore, Queue: outbox, Clock: d.Clock, IDs: d.IDs,
	}
	sdkKeys := appdelivery.SDKKeyDeps{
		Tx: tx, Store: deliveryStore, Audit: audit, Clock: d.Clock, IDs: d.IDs, Random: rand.Reader,
	}
	configuration := appflags.ConfigurationDeps{Tx: tx, Projects: flagStore, Store: flagStore, Rulesets: rulesets, Recorder: flagRec}
	owners := appaccess.NewOwnership(accessStore)
	experimentStore := pgexperiments.NewStore(d.Pool)
	experimentRec := appexperiments.NewRecorder(audit, d.Clock, d.IDs)
	// Stale flags are judged from what the events context counts and the
	// experiments context's running experiments.
	listStaleFlags := appflags.NewListStaleFlags(appflags.StaleDeps{
		Snapshots: tx, Store: flagStore, Evaluations: eventStore, Summaries: eventStore, Experiments: experimentStore,
		Owners: owners, Clock: d.Clock,
	})
	digest := appgovernance.DigestDeps{
		Tx: tx, Store: pggovernance.NewDigests(d.Pool), Report: staleFlagReport{listStaleFlags}, Outbox: outbox,
		Clock: d.Clock, IDs: d.IDs,
	}
	updateFlag := appflags.NewUpdateFlag(appflags.UpdateFlagDeps{
		Tx: tx, Projects: flagStore, Store: flagStore, Owners: owners, Rulesets: rulesets, Recorder: flagRec,
		Evaluations: eventStore,
	})
	updateSegment := appflags.NewUpdateSegment(appflags.UpdateSegmentDeps{
		Tx: tx, Projects: flagStore, Declarations: ws, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
	})
	// A revert finds the version it writes again in the audit log.
	revertConfiguration := appflags.NewRevertConfiguration(appflags.RevertConfigurationDeps{
		Tx: tx, Projects: flagStore, Declarations: ws, Store: flagStore, History: auditLog, Rulesets: rulesets, Recorder: flagRec,
	})
	updateExperiment := appexperiments.NewUpdateExperiment(appexperiments.UpdateExperimentDeps{
		Tx: tx, Projects: experimentStore, Store: experimentStore, Flags: flagStore, UnitTypes: ws, Owners: owners,
		Holders: flagStore, Rulesets: rulesets, Recorder: experimentRec,
	})
	// A start moves on the rulesets of the environments whose flag
	// configurations hold the experiment, which the flags context reads.
	startExperiment := appexperiments.NewStartExperiment(appexperiments.StartExperimentDeps{
		Tx: tx, Projects: experimentStore, Store: experimentStore, Flags: flagStore, UnitTypes: ws,
		Holders: flagStore, Rulesets: rulesets, Recorder: experimentRec,
	})
	// A stop rewrites the rules serving the experiment, which belong to
	// the flags context, each as a configuration write of its own.
	stopExperiment := appexperiments.NewStopExperiment(appexperiments.StopExperimentDeps{
		Tx: tx, Projects: experimentStore, Store: experimentStore, Recorder: experimentRec,
		Holders: flagStore, FirstExposures: eventStore, Checks: experimentStore,
		Rules: appflags.NewApplyExperimentDecision(appflags.ApplyExperimentDecisionDeps{
			Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
	})
	// A change request proposes a configuration or a definition the flags
	// context checks as it would check writing it, and writes it when the
	// change request is applied; a definition's approval covers the
	// environments the workspace context says are protected. An
	// experiment's transition is checked and run by the experiments
	// context, based on the rules serving it the flags context reads.
	changeRequests := appgovernance.ChangeRequestDeps{
		Tx: tx, Projects: changeRequestStore, Store: changeRequestStore,
		Configurations: appflags.NewDraftConfiguration(ws, flagStore, d.IDs),
		Proposals: appflags.NewApplyProposal(appflags.ReplaceConfigurationDeps{
			Tx: tx, Projects: flagStore, Declarations: ws, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
		Reverts:      revertConfiguration,
		Definitions:  appflags.NewDefinitions(updateFlag, updateSegment, appflags.NewSegmentReferrers(flagStore)),
		Experiments:  appexperiments.NewTransitions(experimentStore, startExperiment, stopExperiment, updateExperiment),
		Holders:      appflags.NewExperimentHolders(flagStore),
		Environments: appworkspace.NewProtectedEnvironments(ws),
		Audit:        audit, Clock: d.Clock, IDs: d.IDs, MinLead: o.Schedule.MinLead,
	}
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
	// The attributes evaluated contexts carry undeclared are gathered per
	// replica and written in rounds, so no evaluation waits on recording.
	discoveries := appdelivery.NewDiscoveries(tx, deliveryStore)
	createAttribute := appworkspace.NewCreateAttribute(tx, ws, rulesets, rec)
	managementOptions := o.Options
	managementOptions.Clock = d.Clock
	management.Mount(edge, management.Handlers{
		AuthenticateToken:   appaccess.NewAuthenticateToken(accessStore, tokenUses, d.Clock),
		AuthenticateSession: appaccess.NewAuthenticateSession(accessStore, settings, d.Clock, sessionKey),
		SignIn: appaccess.NewSignIn(appaccess.SignInDeps{
			Tx: tx, Store: accessStore, Settings: settings, Hasher: hasher, Audit: audit, Clock: d.Clock, IDs: d.IDs,
			Random: rand.Reader, Key: sessionKey,
		}),
		SignOut:            appaccess.NewSignOut(tx, accessStore, audit, d.Clock, d.IDs),
		ListSessions:       appaccess.NewListSessions(accessStore, settings, d.Clock),
		RevokeSessions:     appaccess.NewRevokeSessions(tx, accessStore, settings, audit, d.Clock, d.IDs),
		GetAuthSettings:    appaccess.NewGetAuthSettings(settings),
		UpdateAuthSettings: appaccess.NewUpdateAuthSettings(tx, accessStore, settings, audit, d.Clock, d.IDs),
		ListMembers:        appaccess.NewListMembers(accessStore),
		GetMember:          appaccess.NewGetMember(accessStore),
		UpdateMember:       appaccess.NewUpdateMember(tx, accessStore, audit, d.Clock, d.IDs),
		UpdateMe:           appaccess.NewUpdateMe(tx, accessStore, audit, d.Clock, d.IDs, config.ConsoleLocales),
		ChangeMyPassword: appaccess.NewChangeMyPassword(appaccess.ChangeMyPasswordDeps{
			Tx: tx, Store: accessStore, Settings: settings, Hasher: hasher, Audit: audit, Clock: d.Clock, IDs: d.IDs,
		}),
		DeactivateMember:  appaccess.NewDeactivateMember(tx, accessStore, audit, d.Clock, d.IDs),
		ReactivateMember:  appaccess.NewReactivateMember(tx, accessStore, audit, d.Clock, d.IDs, rand.Reader),
		InviteMember:      appaccess.NewInviteMember(tx, accessStore, audit, d.Clock, d.IDs, rand.Reader),
		ReissueInvitation: appaccess.NewReissueInvitation(tx, accessStore, audit, d.Clock, d.IDs, rand.Reader),
		GetInvitation:     appaccess.NewGetInvitation(accessStore, d.Clock),
		AcceptInvitation: appaccess.NewAcceptInvitation(appaccess.AcceptInvitationDeps{
			Tx: tx, Store: accessStore, Settings: settings, Hasher: hasher, Audit: audit, Clock: d.Clock, IDs: d.IDs,
			Random: rand.Reader, Key: sessionKey,
		}),
		ListPersonalTokens:  appaccess.NewListPersonalTokens(accessStore),
		CreatePersonalToken: appaccess.NewCreatePersonalToken(tx, accessStore, settings, audit, d.Clock, d.IDs, rand.Reader),
		RevokePersonalToken: appaccess.NewRevokePersonalToken(tx, accessStore, audit, d.Clock, d.IDs),
		ListProjectMembers:  appaccess.NewListProjectMembers(accessStore),
		CountProjectMembers: appaccess.NewCountProjectMembers(accessStore),
		ListGrants:          appaccess.NewListGrants(accessStore),
		CreateGrant:         appaccess.NewCreateGrant(tx, accessStore, audit, d.Clock, d.IDs),
		DeleteGrant:         appaccess.NewDeleteGrant(tx, accessStore, audit, d.Clock, d.IDs),
		ListProjectTokens:   appaccess.NewListProjectTokens(accessStore),
		CreateProjectToken:  appaccess.NewCreateProjectToken(tx, accessStore, settings, audit, d.Clock, d.IDs, rand.Reader),
		RevokeProjectToken:  appaccess.NewRevokeProjectToken(tx, accessStore, audit, d.Clock, d.IDs),
		ProjectPermissions:  appaccess.NewProjectPermissions(accessStore),
		WorkingProjects:     appaccess.NewWorkingProjects(accessStore),

		FindProject:   appworkspace.NewFindProject(ws),
		ListProjects:  appworkspace.NewListProjects(ws),
		CreateProject: appworkspace.NewCreateProject(tx, ws, rec),
		UpdateProject: appworkspace.NewUpdateProject(tx, ws, rec),
		DeleteProject: appworkspace.NewDeleteProject(tx, ws, rec),

		ListEnvironments:         appworkspace.NewListEnvironments(ws),
		GetEnvironment:           appworkspace.NewGetEnvironment(ws),
		CreateEnvironment:        appworkspace.NewCreateEnvironment(tx, ws, appflags.NewConfigureEnvironment(flagStore), rec),
		UpdateEnvironment:        appworkspace.NewUpdateEnvironment(tx, ws, rec),
		SetEnvironmentProtection: appworkspace.NewSetEnvironmentProtection(tx, ws, rulesets, rec),
		DeleteEnvironment:        appworkspace.NewDeleteEnvironment(tx, ws, rec),

		ListUnitTypes:  appworkspace.NewListUnitTypes(tx, ws, declarationUsages),
		GetUnitType:    appworkspace.NewGetUnitType(ws),
		CreateUnitType: appworkspace.NewCreateUnitType(tx, ws, rulesets, rec),
		UpdateUnitType: appworkspace.NewUpdateUnitType(tx, ws, rec),
		DeleteUnitType: appworkspace.NewDeleteUnitType(tx, ws, declarationGuard, rulesets, rec),

		ListAttributes:  appworkspace.NewListAttributes(tx, ws, declarationUsages),
		GetAttribute:    appworkspace.NewGetAttribute(ws),
		CreateAttribute: createAttribute,
		UpdateAttribute: appworkspace.NewUpdateAttribute(tx, ws, rec),
		DeleteAttribute: appworkspace.NewDeleteAttribute(tx, ws, declarationGuard, rulesets, rec),

		// Flags show when SDKs last evaluated them, which the events
		// context keeps.
		ListFlags: appflags.NewListFlags(tx, ws, flagStore, eventStore),
		CreateFlag: appflags.NewCreateFlag(appflags.CreateFlagDeps{
			Tx: tx, Projects: flagStore, Environments: ws, Store: flagStore, Owners: owners, Rulesets: rulesets, Recorder: flagRec,
		}),
		GetFlag:             appflags.NewGetFlag(tx, flagStore, eventStore),
		ListStaleFlags:      listStaleFlags,
		SendStaleFlagDigest: appgovernance.NewSendStaleFlagDigest(digest),
		UpdateFlag:          updateFlag,

		ArchiveFlag: appflags.NewArchiveFlag(appflags.ArchiveFlagDeps{
			Tx: tx, Projects: flagStore, Store: flagStore, Rulesets: rulesets, Recorder: flagRec, Evaluations: eventStore,
		}),
		RestoreFlag: appflags.NewRestoreFlag(appflags.ArchivalDeps{
			Tx: tx, Projects: flagStore, Store: flagStore, Rulesets: rulesets, Recorder: flagRec, Evaluations: eventStore,
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
		RemoveOverride:      appflags.NewRemoveOverride(configuration),
		RegenerateSalt:      appflags.NewRegenerateSalt(configuration),
		RevertConfiguration: revertConfiguration,
		CopyConfiguration: appflags.NewCopyConfiguration(appflags.ReplaceConfigurationDeps{
			Tx: tx, Projects: flagStore, Declarations: ws, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
		// The tester evaluates through the delivery context, which asks
		// the flags context to check a draft as a write would.
		Evaluate: appdelivery.NewEvaluate(appdelivery.EvaluateDeps{
			Snapshots: tx, Environments: deliveryStore, Declarations: ws, Flags: flagStore,
			Drafts:      appflags.NewDraftConfiguration(ws, flagStore, d.IDs),
			Discoveries: discoveries, Clock: d.Clock,
		}),
		ReadRuleset:      appdelivery.NewReadRuleset(deliveryStore, rulesetCache.Unmeasured()),
		ListSDKInventory: appdelivery.NewListSDKInventory(deliveryStore, deliveryStore),

		ListDiscoveredAttributes:   appdelivery.NewListDiscoveredAttributes(deliveryStore),
		DismissDiscoveredAttribute: appdelivery.NewDismissDiscoveredAttribute(tx, deliveryStore),
		// A discovered attribute is declared through the workspace context,
		// as a member creating it would.
		DeclareDiscoveredAttribute: appdelivery.NewDeclareDiscoveredAttribute(tx, deliveryStore,
			func(ctx context.Context, a appdelivery.AttributeDeclaration) (workspace.Attribute, error) {
				return createAttribute.Handle(ctx, appworkspace.CreateAttributeCommand{
					Project: a.Project, Key: a.Key, Name: a.Name, Description: a.Description, Type: a.Type, Request: a.Request,
				})
			}),

		ListSDKKeys:  appdelivery.NewListSDKKeys(deliveryStore, d.Clock),
		CreateSDKKey: appdelivery.NewCreateSDKKey(sdkKeys),
		UpdateSDKKey: appdelivery.NewUpdateSDKKey(sdkKeys),
		RotateSDKKey: appdelivery.NewRotateSDKKey(sdkKeys),
		RevokeSDKKey: appdelivery.NewRevokeSDKKey(sdkKeys),

		ListExperiments: appexperiments.NewListExperiments(experimentStore, d.Clock),
		CreateExperiment: appexperiments.NewCreateExperiment(appexperiments.CreateExperimentDeps{
			Tx: tx, Projects: experimentStore, Store: experimentStore, Flags: flagStore, UnitTypes: ws, Rollouts: flagStore,
			Owners: owners, Recorder: experimentRec,
		}),
		GetExperiment:    appexperiments.NewGetExperiment(experimentStore),
		UpdateExperiment: updateExperiment,
		StartExperiment:  startExperiment,
		StopExperiment:   stopExperiment,
		ArchiveExperiment: appexperiments.NewArchiveExperiment(appexperiments.ArchiveExperimentDeps{
			Tx: tx, Projects: experimentStore, Store: experimentStore, Recorder: experimentRec,
		}),
		SetExperimentDecisionNote: appexperiments.NewSetDecisionNote(appexperiments.SetDecisionNoteDeps{
			Tx: tx, Projects: experimentStore, Store: experimentStore, Recorder: experimentRec,
		}),
		DeleteExperiment: appexperiments.NewDeleteExperiment(appexperiments.DeleteExperimentDeps{
			Tx: tx, Projects: experimentStore, Store: experimentStore, Recorder: experimentRec,
		}),
		RegenerateExperimentSalts: appexperiments.NewRegenerateSalts(appexperiments.RegenerateSaltsDeps{
			Tx: tx, Projects: experimentStore, Store: experimentStore, Rollouts: flagStore, Recorder: experimentRec,
		}),
		CloneExperiment: appexperiments.NewCloneExperiment(appexperiments.CloneExperimentDeps{
			Tx: tx, Projects: experimentStore, Store: experimentStore, Flags: flagStore, UnitTypes: ws, Rollouts: flagStore,
			Owners: owners, Recorder: experimentRec,
		}),
		GetExperimentAssignment: appexperiments.NewGetAssignment(experimentStore),
		CheckHashUniformity:     appexperiments.NewCheckUniformity(),
		ListExperimentAlerts:    appexperiments.NewListAlerts(experimentStore),
		AcknowledgeExperimentAlert: appexperiments.NewAcknowledgeAlert(appexperiments.AcknowledgeAlertDeps{
			Tx: tx, Store: experimentStore, Recorder: experimentRec,
		}),
		GetSampleRatio: appexperiments.NewGetSampleRatio(experimentStore),

		GetExperimentExposures: appevents.NewGetExperimentExposures(tx, eventStore),
		GetExposureTimeline:    appevents.NewGetExposureTimeline(tx, eventStore),
		GetConflicts:           appevents.NewGetConflicts(tx, eventStore),
		GetFlagEvaluations:     appevents.NewGetFlagEvaluations(tx, eventStore, d.Clock),
		GetEventVolume:         appevents.NewGetEventVolume(tx, eventStore, d.Clock),

		ListChangeRequests:   appgovernance.NewListChangeRequests(changeRequestStore),
		CreateChangeRequest:  appgovernance.NewCreateChangeRequest(changeRequests),
		GetChangeRequest:     appgovernance.NewGetChangeRequest(changeRequestStore),
		UpdateChangeRequest:  appgovernance.NewUpdateChangeRequest(changeRequests),
		OpenChangeRequest:    appgovernance.NewOpenChangeRequest(changeRequests),
		RebaseChangeRequest:  appgovernance.NewRebaseChangeRequest(changeRequests),
		CancelChangeRequest:  appgovernance.NewCancelChangeRequest(changeRequests),
		ApproveChangeRequest: appgovernance.NewApproveChangeRequest(changeRequests),
		CountWaiting:         appgovernance.NewCountWaitingChangeRequests(changeRequestStore),
		RejectChangeRequest:  appgovernance.NewRejectChangeRequest(changeRequests),
		ApplyChangeRequest:   appgovernance.NewApplyChangeRequest(changeRequests),
		CopyChangeRequest:    appgovernance.NewCopyChangeRequest(changeRequests),

		ListWebhookEventTypes: appgovernance.NewListWebhookEventTypes(),
		ListWebhooks:          appgovernance.NewListWebhooks(webhookStore, d.Clock),
		CreateWebhook:         appgovernance.NewCreateWebhook(webhooks),
		GetWebhook:            appgovernance.NewGetWebhook(webhookStore, d.Clock),
		UpdateWebhook:         appgovernance.NewUpdateWebhook(webhooks),
		DeleteWebhook:         appgovernance.NewDeleteWebhook(webhooks),
		ListWebhookDeliveries: appgovernance.NewListDeliveries(webhookStore, webhookStore),
		GetWebhookDelivery:    appgovernance.NewGetDelivery(webhookStore, webhookStore),
		TestWebhook:           appgovernance.NewTestWebhook(deliveryRequests),
		RedeliverWebhook:      appgovernance.NewRedeliver(deliveryRequests),
		EnableWebhook:         appgovernance.NewEnableWebhook(webhooks),
		RotateWebhookSecret:   appgovernance.NewRotateWebhookSecret(webhooks),

		ListSinks:        appsinks.NewListSinks(sinks.Store),
		CreateSink:       appsinks.NewCreate(sinks),
		GetSink:          appsinks.NewGetSink(sinks.Store),
		UpdateSink:       appsinks.NewUpdate(sinks),
		DeleteSink:       appsinks.NewDelete(sinks),
		RotateSinkSecret: appsinks.NewRotateSecret(sinks),

		ListSegments: appflags.NewListSegments(tx, flagStore),
		GetSegment:   appflags.NewGetSegment(flagStore),
		CreateSegment: appflags.NewCreateSegment(appflags.CreateSegmentDeps{
			Tx: tx, Projects: flagStore, Declarations: ws, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
		UpdateSegment: updateSegment,
		DeleteSegment: appflags.NewDeleteSegment(appflags.DeleteSegmentDeps{
			Tx: tx, Projects: flagStore, Store: flagStore, Rulesets: rulesets, Recorder: flagRec,
		}),
		ListSegmentUsages: appflags.NewListSegmentUsages(tx, flagStore),

		ListAuditEntries:   appgovernance.NewListAuditEntries(auditLog),
		GetAuditEntry:      appgovernance.NewGetAuditEntry(auditLog),
		ExportAuditEntries: appgovernance.NewExportAuditEntries(tx, auditLog, o.AuditExportMaxEntries),
		StreamChanges:      appgovernance.NewStreamChanges(changeHub, auditLog),
	}, reg, managementOptions)
	// Every replica keeps the SDK keys it authenticated, dropped on a
	// notification of each change, and writes their uses in rounds.
	sdkKeyCache := pgdelivery.NewSDKKeyCache(deliveryStore)
	sdkKeyUses := appdelivery.NewSDKKeyUses(deliveryStore)
	// Every replica offers each new ruleset version to the streams it holds
	// once it has loaded it, woken by the notification of each bump.
	deliveryLog := pggovernance.NewDeliveries(d.Pool)
	rulesetStream := pgdelivery.NewRulesetStream(rulesetCache, edge.Logger())
	// Every replica counts the remote evaluations it answers and writes the
	// counts in rounds, and once more when it stops.
	remoteCounts := appdelivery.NewRemoteEvaluationCounts(eventStore, d.Clock)
	// Every replica gathers the SDKs its requests come from and writes them
	// in rounds.
	inventory := appdelivery.NewSDKInventory(tx, deliveryStore, d.Clock)
	rollupMetrics := worker.NewRollupMetrics(reg)
	var ingestTx appevents.Transactor = tx
	if o.IngestHold != nil {
		ingestTx = o.IngestHold.Wrap(tx, d.Pool)
	}
	sdk.Mount(edge, sdk.Handlers{
		Authenticate: appdelivery.NewAuthenticateSDKKey(sdkKeyCache, sdkKeyUses, d.Clock),
		ServeRuleset: appdelivery.NewServeRuleset(rulesetCache),
		WatchRuleset: appdelivery.NewWatchRuleset(rulesetStream, rulesetCache.Unmeasured()),
		Evaluate:     appdelivery.NewEvaluateRemotely(rulesetCache, discoveries, remoteCounts, d.Clock),
		Ingest:       appevents.NewIngestEvents(ingestTx, eventStore, d.Clock, o.EventsRetention),
		Inventory:    inventory,
	}, sdk.Options{Clock: d.Clock, RateLimits: o.RateLimits, Registerer: reg})
	return Background{
		Workers: []worker.Worker{
			worker.AuthRetention(appaccess.NewPurgeAuthRecords(tx, accessStore, settings, d.Clock), reg),
			worker.TokenLastUse(tokenUses),
			worker.SDKKeyLastUse(sdkKeyUses),
			worker.DiscoveredAttributes(discoveries, edge.Logger()),
			worker.RemoteEvaluationCounts(remoteCounts, edge.Logger()),
			worker.SDKInventory(inventory, edge.Logger(), reg),
			worker.RollupStream(worker.ExposureRollupName, events.StreamExposures,
				appevents.NewRollUpExposures(tx, eventStore, d.Clock), rollupMetrics),
			worker.RollupStream(worker.SummaryRollupName, events.StreamEvaluationSummaries,
				appevents.NewRollUpSummaries(tx, eventStore, d.Clock), rollupMetrics),
			worker.RollupStream(worker.AliasRollupName, events.StreamAliases,
				appevents.NewRollUpAliases(tx, eventStore, d.Clock), rollupMetrics),
			worker.EventRetention(
				appevents.NewRunRetention(tx, eventStore, d.Clock, d.IDs, o.EventsRetention, o.EventsPartitionsAhead),
				appdelivery.NewPurgeSDKInventory(deliveryStore, d.Clock, o.SDKInventoryRetention), edge.Logger(), reg),
			worker.WebhookFanOut(appgovernance.NewRunFanOut(tx, outbox, d.Clock, d.IDs), o.WebhookTick, reg),
			worker.WebhookDelivery(appgovernance.NewDeliver(appgovernance.DeliverDeps{
				Tx: tx, Store: deliveryLog, Sender: webhook.NewSender(o.WebhookTargets),
				Opener: o.Keyring, Clock: d.Clock, IDs: d.IDs, Random: rand.Reader, Audit: audit, Outbox: outbox,
			}), o.WebhookTick, edge.Logger(), reg),
			worker.WebhookRetention(appgovernance.NewRunWebhookRetention(tx, deliveryLog, d.Clock), reg),
			worker.StaleFlagDigest(appgovernance.NewRunDigestRound(digest, o.DigestSlot).Handle),
			worker.ProjectPurge(appworkspace.NewPurgeDeletedProjects(tx, ws)),
			worker.ChangeRequestScheduler(o.Schedule.Tick,
				appgovernance.NewRunScheduler(changeRequests, changeRequestStore, o.Schedule.MaxFailures).Handle),
			worker.ExperimentChecks(appexperiments.CheckInterval, appexperiments.NewRunChecks(appexperiments.RunChecksDeps{
				Tx: tx, Lock: experimentStore, Running: experimentStore, Pending: experimentStore, Final: eventStore, Retention: o.EventsRetention,
				Projects: experimentStore, Store: experimentStore,
				Holders: flagStore, FirstExposures: eventStore, Exposures: eventStore, Checks: experimentStore, Recorder: experimentRec,
			}).Handle),
		},
		Listeners: []Listener{
			{Channel: pgaccess.AuthSettingsChannel, Handler: settings},
			{Channel: pgdelivery.SDKKeysChannel, Handler: sdkKeyCache},
			{Channel: pgdelivery.RulesetsChannel, Handler: rulesetStream},
			{Channel: pggovernance.ChangesChannel, Handler: changeHub},
		},
		Close: func() {
			rulesetStream.Close()
			changeHub.Close()
			rulesetCache.Close()
			guardPool.Close()
			// The requests are done: what they counted since the last round
			// is written now rather than lost with the replica.
			ctx, cancel := context.WithTimeout(context.Background(), finalFlushBound)
			defer cancel()
			if _, err := remoteCounts.Flush(ctx); err != nil {
				edge.Logger().Warn("remote evaluation counts lost on stop", "error", err.Error())
			}
			if err := inventory.Flush(ctx); err != nil {
				edge.Logger().Warn("SDK inventory lost on stop", "error", err.Error())
			}
		},
	}
}

// Hasher returns the password hasher of a command line, which hashes one
// password and needs no bound.
func Hasher() *password.Hasher { return password.NewHasher(password.DefaultParams) }

// Bootstrap returns the use case creating the first instance admin.
func Bootstrap(d Deps) *appaccess.Bootstrap {
	return appaccess.NewBootstrap(postgres.NewTransactor(d.Pool), pgaccess.NewStore(d.Pool),
		Hasher(), record(d),
		d.Clock, d.IDs, rand.Reader)
}

// CreateInstanceAdmin returns the use case adding an instance admin from the
// command line.
func CreateInstanceAdmin(d Deps) *appaccess.CreateInstanceAdmin {
	return appaccess.NewCreateInstanceAdmin(postgres.NewTransactor(d.Pool), pgaccess.NewStore(d.Pool), Hasher(),
		record(d), d.Clock, d.IDs, rand.Reader)
}

// PromoteMember returns the use case making a member an instance admin from
// the command line.
func PromoteMember(d Deps) *appaccess.PromoteMember {
	return appaccess.NewPromoteMember(postgres.NewTransactor(d.Pool), pgaccess.NewStore(d.Pool),
		record(d), d.Clock, d.IDs)
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

// record returns the audit port of a use case run from the command line,
// recording through the governance context like every other.
func record(d Deps) *appgovernance.Record {
	return appgovernance.NewRecord(pggovernance.NewAuditLog(d.Pool), pggovernance.NewChangeRequests(d.Pool),
		appflags.NewSegmentReferrers(pgflags.NewStore(d.Pool)), d.IDs,
		appgovernance.WithOutbox(pggovernance.NewOutbox(d.Pool), d.IDs))
}
