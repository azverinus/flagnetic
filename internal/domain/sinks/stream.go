package sinks

import (
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
)

// Scope narrows a stream to the rows of one project and, when Environments is
// not empty, of those environments, and, when Floor is set, to the rows
// received at or after it. It is part of the bound a batch is chosen by and of
// the lag, never only of the read, so that a stream's cursor moves past the
// rows it will never send. A sink naming environments whose every one is gone
// has no scope, since an empty list would read the whole project: it reads
// nothing until it is edited.
type Scope struct {
	Project      workspace.ProjectID
	Environments []workspace.EnvironmentID
	// Floor leaves out the rows received before it; nil leaves out none.
	Floor *time.Time
}

// Batch is the rows of one stream past one cursor up to and including another,
// all of transactions that had ended when it was chosen, so every read of it
// finds the same rows.
type Batch struct {
	From, To events.Cursor
	// Rows is how many rows the range holds in the stream's scope.
	Rows int
}

// Page is the rows of one batch as they are exported.
type Page struct {
	// Rows are the rows of the batch, in the order they were committed.
	Rows []Row
	// Skipped is how many rows of the batch were passed over because their
	// environment no longer exists: the key they would be exported under is
	// gone with it.
	Skipped int
}

// FailureAt is a failed call and when it was made.
type FailureAt struct {
	Failure
	At time.Time
}

// Stream is the delivery of one event kind of a sink: where it has read to
// and how its last calls went. A sink has one per event kind it exports, from
// the moment it is first enabled.
type Stream struct {
	EventKind EventKind
	// Cursor is the position of the last row delivered.
	Cursor events.Cursor
	// LastDeliveredAt is when a batch was last acknowledged; nil before the
	// first.
	LastDeliveredAt *time.Time
	// LastFailure is the last call that failed since the stream last
	// delivered; nil when it has delivered since, or never failed.
	LastFailure *FailureAt
	// ConsecutiveFailures is how many calls in a row failed; zero once one
	// is acknowledged. A stream with any is failing.
	ConsecutiveFailures int
	// Stop and Resume are set while a bounded replay runs: the last row it
	// sends, and the position the stream returns to after it.
	Stop, Resume *events.Cursor
	// Floor is the time a stream started from a time reads from: no row
	// received before it is sent, nil for a stream that sends every row past
	// its cursor. The cursor alone cannot say it. A stream starting from a
	// time starts no later than the snapshot horizon, so that a transaction
	// still open then is read once it commits; but the horizon is held back
	// by any transaction open anywhere in the database, so the rows between
	// it and the first one received since the start are read too, and must
	// not be sent.
	Floor *time.Time
}

// ReadScope is scope as the stream chooses and reads its next batch: with the
// stream's floor, except while a bounded replay runs, which sends the rows of
// the window it was asked for whenever they were received relative to the
// floor. The live stream the replay returns to keeps the floor.
func (s Stream) ReadScope(scope Scope) Scope {
	scope.Floor = s.Floor
	if s.Stop != nil {
		scope.Floor = nil
	}
	return scope
}

// LiveScope is scope as the stream's live position reads it, which its lag is
// measured by: with the stream's floor, a bounded replay running or not.
func (s Stream) LiveScope(scope Scope) Scope {
	scope.Floor = s.Floor
	return scope
}

// Failing reports whether the stream's last call failed.
func (s Stream) Failing() bool { return s.ConsecutiveFailures > 0 }

// Backoff is how long a stream waits before the call after its failures'th
// consecutive failed one: a second for the first, doubling, at most five
// minutes, spread by 20 % either way so that streams failing together do not
// retry in lockstep. random returns a number in [0, 1).
func Backoff(failures int, random func() float64) time.Duration {
	const (
		first  = time.Second
		ceil   = 5 * time.Minute
		spread = 0.2
	)
	d := first
	for range max(failures-1, 0) {
		d *= 2
		if d >= ceil {
			d = ceil
			break
		}
	}
	d = time.Duration(float64(d) * (1 - spread + 2*spread*random()))
	return min(d, ceil)
}

// OutOfRetention reports whether a sink asked to start at from, at now, would
// start before the oldest event that is still stored: events are kept for
// retention, so what is older is gone or about to be.
func OutOfRetention(from, now time.Time, retention time.Duration) bool {
	return from.Before(now.Add(-retention))
}
