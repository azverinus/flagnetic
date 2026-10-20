// Package flagnetic is the Flagnetic SDK for Go services: it holds a
// ruleset in memory and evaluates flags locally with the conformance-verified
// evaluator in github.com/azverinus/flagnetic/eval, the same one the
// server's remote evaluation uses.
//
// The module depends on that evaluator and the standard library, nothing
// else. A Client is safe for concurrent use; the ruleset swaps through an
// atomic pointer and evaluations never lock it.
//
// # Fetching and the fallback chain
//
// New with a server SDK key and the base URL of a Flagnetic instance
// fetches the ruleset in the background and polls it with If-None-Match.
// Ready returns the stage reached at the first fetch result or at its
// context's deadline. The stage is fresh after a success, last_good after a
// failure with a ruleset held (from an earlier fetch or from WithStore), and
// built_in with nothing held. A failure never replaces or discards the held
// ruleset: it logs fetch_failed and retries with exponential backoff and
// full jitter. A revoked key logs key_revoked and is retried more slowly.
//
// # The change stream
//
// The client also opens the change stream, an SSE connection carrying
// nothing but new ruleset versions, before its first fetch, so that an
// operator's kill switch reaches the service within two seconds. A version
// above the held one causes a fetch with If-None-Match; a replica still
// behind is asked again after 1, 2 and 4 seconds, and the held version
// never goes down. A dropped stream reconnects with Last-Event-ID set to
// the held version, and every reconnect is followed by a fetch, because
// versions may have been missed. The safety poll runs every 5 minutes while
// the stream is connected and every 30 seconds while it is down or turned
// off with WithChangeStream(false), which is what a proxy blocking
// text/event-stream leaves.
//
//	c, err := flagnetic.New(sdkKey, "https://flags.example.com", flagnetic.WithStore("ruleset.json"))
//	stage := c.Ready(ctxWithTimeout)
//	defer c.Close(context.Background())
//
// # Evaluation summaries
//
// Every evaluation increments a counter keyed by flag key, variation and
// evaluation reason, fallback and local_override included, and every 60
// seconds the window becomes one evaluation summary: a UUIDv7 event ID,
// the interval's bounds, the ruleset version held at its end and the
// counts, gzipped into the ingest envelope of the events endpoint with
// this SDK's name, version and platform. It carries no unit ID, no
// attribute and no value, so a report can name a stale flag and a version
// in the wild without telling the platform who was evaluated. Flush sends
// the current window and Close flushes within five seconds;
// WithSummaries(false) turns reporting off. A batch answered 429, a 5xx or
// nothing at all is sent again whole, with the same event IDs, so a retry
// is deduplicated on receipt; a batch still unsent when the interval is up
// merges into the next window, and a window kept for over an hour is
// dropped rather than delivered late. A refused key logs key_revoked once
// and holds reporting back at a backoff growing to the unauthorized cap,
// indefinitely, so an operator restoring it needs no redeploy.
//
// # Exposures and aliases
//
// Evaluating a flag is not an exposure, so being assigned a variation is
// never mistaken for having seen it. Exposure is called
// where the served variation actually affects what the unit sees: it looks
// up the most recent detail of that flag for that context and records an
// exposure only when the detail carries an experiment block, logging
// exposure_skipped at debug otherwise, naming no_experiment, no_detail
// or repeat. A later ruleset does not invalidate
// what was remembered, but an evaluation that no longer carries an
// experiment block does, so a stopped experiment stops being reported. The
// event carries a UUIDv7 chosen at that moment and unchanged across every
// retry, the experiment, flag and variation keys, the unit type and ID,
// the context's platform and app_version, the evaluation reason
// (experiment or override), the experiment block's override marker and
// the ruleset version; the SDK's name and version are the envelope's. A flag whose automatic_exposure is
// set records one after every evaluation that yields an experiment block
// instead, for backend algorithms where evaluation is the exposure. A
// repeat per experiment, unit and variation is suppressed by a bounded
// in-process set for at least the flush interval, and the platform's own
// deduplication is what analysis relies on.
//
// Alias links two unit IDs of the same person, typically an anonymous ID
// and a user ID after sign-up. It changes no context: an experiment
// crossing registration keeps hashing on the unit type it declared, and
// the alias is what lets analysis join the two.
//
//	d := c.Detail(ctx, fc, "checkout_v2", false)
//	if d.Value == true { renderNewCheckout() }
//	c.Exposure(ctx, fc, "checkout_v2")
//	c.Alias(ctx, "anonymous", anonymousID, "user", userID)
//
// Both ride one in-memory queue, flushed every 2 seconds or at 500 events,
// holding 10 000 and dropping the oldest with queue_dropped. A batch
// answered 429, a 5xx or nothing at all is sent again whole with the same
// event IDs; events the platform rejected are never sent again.
// WithExposures(false) turns both off, with a warning at construction.
//
// # Offline mode
//
// A client constructed with WithOffline, WithOfflineFile or
// WithOfflineSource reads a ruleset document exactly as the ruleset
// endpoint serves it and never touches the network. Its stage is fresh,
// Ready returns at once, and what it would report goes to an in-memory
// Recorder: Summary is the running count, EvaluationSummaries the reports
// a flush would have sent, and Exposures and Aliases the events the calls
// above recorded.
//
//	c, err := flagnetic.New("", "", flagnetic.WithOfflineFile("ruleset.json"))
//	fc := flagnetic.Context{Units: map[string]string{"user": "u-1"}}
//	enabled := c.Bool(ctx, fc, "new_checkout", false)
//
// # Getters
//
// Bool, String, Number, Int, JSON and Detail take a context.Context first,
// as any Go call on a request path does, then the flag context, the flag
// key and the built-in default. They are total: they never panic and never
// fail. A missing flag, a flag whose type differs from the getter's, an
// evaluator error, or stage built_in serves the built-in default passed at
// the call site with reason fallback. Int serves a number flag only when
// the value is whole and fits in an int64, and the default with
// type_mismatch otherwise. Evaluation never records an exposure, except for
// a flag whose automatic_exposure is set.
//
// # Local overrides
//
// With WithLocalOverrides, Client.LocalOverrides substitutes flag values
// inside this process, for a developer's machine or a non-production build. An
// overridden flag reads reason local_override with no experiment block. A
// ruleset whose environment is protected refuses local overrides.
package flagnetic
