package sinks

import (
	"context"
	"fmt"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// Tracked returns every sink that has a stream, or is marked at risk, with its
// streams, the disabled and the stopped included: they are the sinks whose
// undelivered rows retention can still drop.
func (s *Store) Tracked(ctx context.Context) ([]sinks.Sink, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT `+sinkColumns+sinkFrom+`
		WHERE s.at_risk_since IS NOT NULL OR EXISTS (SELECT 1 FROM sink_streams ss WHERE ss.sink_id = s.id)
		ORDER BY s.id`)
	if err != nil {
		return nil, fmt.Errorf("listing the sinks with streams: %w", err)
	}
	defer rows.Close()
	out := []sinks.Sink{}
	for rows.Next() {
		v, scanErr := scanSink(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("reading a sink: %w", scanErr)
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	return out, s.withStreams(ctx, out)
}

// EnterAtRisk marks sink at risk since now and reports whether it changed:
// false when it was at risk already or is gone, so that of replicas judging it
// at once only one tells of the episode.
func (s *Store) EnterAtRisk(ctx context.Context, sink sinks.ID, now time.Time) (bool, error) {
	tag, err := s.db(ctx).Exec(ctx, `UPDATE sinks SET at_risk_since = $2 WHERE id = $1 AND at_risk_since IS NULL`, sink.Bytes(), now)
	if err != nil {
		return false, fmt.Errorf("marking a sink at risk: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// LeaveAtRisk clears sink's mark of being at risk.
func (s *Store) LeaveAtRisk(ctx context.Context, sink sinks.ID) error {
	if _, err := s.db(ctx).Exec(ctx, `UPDATE sinks SET at_risk_since = NULL WHERE id = $1 AND at_risk_since IS NOT NULL`, sink.Bytes()); err != nil {
		return fmt.Errorf("clearing a sink's mark of being at risk: %w", err)
	}
	return nil
}

// AtRiskStates returns whether each sink is marked at risk.
func (s *Store) AtRiskStates(ctx context.Context) (map[sinks.ID]bool, error) {
	rows, err := s.db(ctx).Query(ctx, `SELECT id, at_risk_since IS NOT NULL FROM sinks`)
	if err != nil {
		return nil, fmt.Errorf("reading which sinks are at risk: %w", err)
	}
	defer rows.Close()
	out := map[sinks.ID]bool{}
	for rows.Next() {
		var (
			sk     [16]byte
			atRisk bool
		)
		if err = rows.Scan(&sk, &atRisk); err != nil {
			return nil, fmt.Errorf("reading whether a sink is at risk: %w", err)
		}
		out[id.FromBytes[sinks.Sink](sk)] = atRisk
	}
	return out, rows.Err()
}
