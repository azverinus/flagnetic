package sinks

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/events"
	"github.com/azverinus/flagnetic/internal/domain/workspace"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// DeadLetterID identifies a dead letter.
type DeadLetterID = id.Of[DeadLetter]

// DeadLetterStatus says where a dead letter stands.
type DeadLetterStatus string

// The statuses of a dead letter.
const (
	// DeadLetterPending waits to be redelivered or discarded.
	DeadLetterPending DeadLetterStatus = "pending"
	// DeadLetterRedelivered was acknowledged by the target after a
	// redelivery.
	DeadLetterRedelivered DeadLetterStatus = "redelivered"
	// DeadLetterExpired lost every row to retention.
	DeadLetterExpired DeadLetterStatus = "expired"
)

// DeadLetter is a batch of one event kind that the target rejected over and
// over, set aside so that its stream could go on. It keeps the range of rows
// and the environments the batch was filtered to, so that the rows still
// stored can be sent again, and says how the batch ended: how many times it
// was rejected and why the last time.
type DeadLetter struct {
	ID        DeadLetterID
	Sink      ID
	EventKind EventKind
	// From is the position before the batch's first row and To that of its
	// last row.
	From, To events.Cursor
	// Environments are the ones the batch was filtered to; none means every
	// one.
	Environments []workspace.EnvironmentID
	// Rows is how many rows the batch held when it was set aside, and
	// FirstReceivedAt and LastReceivedAt when the first and the last of them
	// were received.
	Rows                            int
	FirstReceivedAt, LastReceivedAt time.Time
	// Attempts is how many times the target rejected the batch, a failed
	// redelivery included.
	Attempts    int
	LastFailure FailureAt
	Status      DeadLetterStatus
	CreatedAt   time.Time
	// RedeliveredAt is when a redelivery was acknowledged; nil before.
	RedeliveredAt *time.Time
}

// SetsAside reports whether the rejection a batch is now given is the one that
// sets it aside as a dead letter: the limit'th rejection of one batch in
// flight, priorRejections being those before it. Only a rejection counts; a
// target that is down is waited for without limit.
func SetsAside(priorRejections, limit int) bool { return priorRejections+1 >= limit }

// StopsAfter reports whether inARow dead letters in a row on one stream stop
// its sink: a target that keeps refusing is not fed the rest of the event
// store. A batch the target acknowledged ends the run.
func StopsAfter(inARow, limit int) bool { return inARow >= limit }

// RedeliveryID is the delivery ID of the redelivery of dead letter dl made
// when it had been rejected attempts times: its own, so that the target sees
// a new delivery and not one of the batch's resends, and the same for the same
// attempt.
func RedeliveryID(dl DeadLetterID, attempts int) string {
	sum := sha256.Sum256([]byte("redeliver." + dl.String() + "." + strconv.Itoa(attempts)))
	return "dlv_" + hex.EncodeToString(sum[:])[:26]
}
