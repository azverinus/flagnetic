package governance

import "slices"

// MaxPendingNotices bounds the notices a change stream holds unsent, and the
// notices replayed after a Last-Event-ID: a client further behind than that
// is told to refetch what it shows, which costs it less than catching up
// notice by notice.
const MaxPendingNotices = 1000

// ChangeNotice is what the management change stream tells of one audit
// entry: the entity it changed and where, never its content, so a client
// refetches what it shows of that entity.
type ChangeNotice struct {
	// Entry is the audit entry's identifier, by which a client resumes.
	Entry  EntryID
	Entity Entity
	// Environment is the key of the environment the change addressed, ""
	// for none.
	Environment string
	// Version is the entity's version after the change, or before it for a
	// deletion; nil for an entity that has none, such as a member.
	Version *int64
	Actor   Actor
}

// noticeIdentity is what notices coalesce by: one entity, by its identifier
// rather than its key, which two entities may share (change requests are
// keyed by their title), in one environment.
type noticeIdentity struct{ kind, entity, environment string }

func (n ChangeNotice) identity() noticeIdentity {
	return noticeIdentity{n.Entity.Kind, n.Entity.ID, n.Environment}
}

// PendingNotices are the notices a change stream has not sent yet. A client
// refetches an entity whatever changed in it, so a notice of an entity
// already waiting replaces that one and takes its place at the back: each
// entity is sent once, in the order of its latest change. Past
// MaxPendingNotices the stream is told to refetch instead. The zero value
// holds nothing.
type PendingNotices struct {
	notices []ChangeNotice
	refetch bool
}

// Offer adds notices behind those waiting.
func (p *PendingNotices) Offer(notices ...ChangeNotice) {
	for _, n := range notices {
		identity := n.identity()
		p.notices = slices.DeleteFunc(p.notices, func(w ChangeNotice) bool { return w.identity() == identity })
		p.notices = append(p.notices, n)
	}
	if len(p.notices) > MaxPendingNotices {
		p.Refetch()
	}
}

// Refetch replaces whatever waits with telling the client to refetch, as
// when a notice may have been missed; notices offered afterwards follow it.
func (p *PendingNotices) Refetch() {
	p.notices, p.refetch = nil, true
}

// Take returns what waits, oldest first, and whether the client must
// refetch first, and leaves nothing waiting.
func (p *PendingNotices) Take() (notices []ChangeNotice, refetch bool) {
	notices, refetch = p.notices, p.refetch
	p.notices, p.refetch = nil, false
	return notices, refetch
}
