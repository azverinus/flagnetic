package worker_test

import (
	"context"
	"slices"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/azverinus/flagnetic/internal/domain/sinks"
	"github.com/azverinus/flagnetic/internal/infra/in/worker"
	"github.com/azverinus/flagnetic/internal/shared/id"
	"github.com/azverinus/flagnetic/internal/testkit"
)

// oneSink answers the scrape-time questions about the one sink the metrics
// below are labelled with.
type oneSink struct{ sink sinks.ID }

func (o oneSink) PendingDeadLetters(context.Context) (map[sinks.ID]int, error) {
	return map[sinks.ID]int{o.sink: 0}, nil
}

func (o oneSink) AtRiskStates(context.Context) (map[sinks.ID]bool, error) {
	return map[sinks.ID]bool{o.sink: false}, nil
}

func TestTheSinkMetricsAreDocumentedWithTheirLabels(t *testing.T) {
	sink, err := id.Parse[sinks.Sink]("0197c1d2-5b3e-7a41-9c0f-3d2e8a6b4f10")
	if err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	m := worker.NewSinkMetrics(registry, oneSink{sink}, oneSink{sink})
	m.Batch(sink, sinks.EventExposure, "ack")
	m.Lag(sink, sinks.EventExposure, 0)
	m.RowsSkipped("environment_deleted", 1)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 5 {
		t.Fatalf("%d metric families, want the five NewSinkMetrics registers, each set above", len(families))
	}
	for _, page := range []string{"sinks.md", "operations.md"} {
		doc := testkit.Doc(t, page)
		for _, f := range families {
			var want []string
			for _, l := range f.GetMetric()[0].GetLabel() {
				want = append(want, l.GetName())
			}
			slices.Sort(want)
			got, ok := testkit.DocMetricLabels(doc, f.GetName())
			if !ok {
				t.Errorf("%s has no metrics row for %s", page, f.GetName())
				continue
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s documents %s with labels %v, registered with %v", page, f.GetName(), got, want)
			}
		}
	}
}
