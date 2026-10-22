package delivery_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/domain/delivery"
	apperrors "github.com/azverinus/flagnetic/internal/shared/errors"
)

func TestOnlyTheKeyAndJSONTypeOfAnUndeclaredAttributeAreObserved(t *testing.T) {
	got := delivery.ObserveUndeclared(remoteRuleset(t), map[string]any{
		"plan": "pro", "seats": "3", // declared, even with a mismatched type
		"user":     "u-1", // a unit type
		"nickname": "Kiwi", "age": 41.0, "vip": true, "labels": []any{"a", 1.0}, "address": map[string]any{"city": "x"},
		"gone": nil, "Not-A-Key": "x", "discovered": "x", strings.Repeat("k", 65): "x",
	})
	want := []delivery.ObservedAttribute{
		{Key: "address", Type: "object"},
		{Key: "age", Type: "number"},
		{Key: "labels", Type: "list"},
		{Key: "nickname", Type: "string"},
		{Key: "vip", Type: "boolean"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("observed = %v\nwant %v", got, want)
	}
}

func TestASightingMergesIntoTheDiscoveredAttribute(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	d := delivery.NewDiscoveredAttribute(project, delivery.Sighting{Key: "nickname", Types: []delivery.ObservedType{"string"}, Count: 2, First: t0, Last: t0.Add(time.Second)})
	if d.Key != "nickname" || d.Count != 2 || !d.FirstSeen.Equal(t0) || !d.LastSeen.Equal(t0.Add(time.Second)) || d.Dismissed {
		t.Fatalf("new = %+v", d)
	}
	d = d.Seen(delivery.Sighting{Key: "nickname", Types: []delivery.ObservedType{"number", "string"}, Count: 3, First: t0.Add(time.Minute), Last: t0.Add(time.Hour)})
	if !reflect.DeepEqual(d.Types, []delivery.ObservedType{"string", "number"}) || d.Count != 5 || !d.FirstSeen.Equal(t0) || !d.LastSeen.Equal(t0.Add(time.Hour)) {
		t.Errorf("merged = %+v, want the types in the order first seen and the counts added", d)
	}

	dismissed, err := d.Dismiss()
	if err != nil || !dismissed.Dismissed {
		t.Fatalf("dismiss = %+v, %v", dismissed, err)
	}
	if _, err := dismissed.Dismiss(); !hasCode(err, apperrors.KindNotFound, "not_found") {
		t.Errorf("dismissing twice: %v, want not_found", err)
	}
	again := dismissed.Seen(delivery.Sighting{Key: "nickname", Types: []delivery.ObservedType{"number"}, Count: 1, First: t0.Add(2 * time.Hour), Last: t0.Add(2 * time.Hour)})
	if !again.Dismissed || again.Count != 6 {
		t.Errorf("seen again with a known type: %+v, want still dismissed and counted", again)
	}
	back := again.Seen(delivery.Sighting{Key: "nickname", Types: []delivery.ObservedType{"boolean"}, Count: 1, First: t0.Add(3 * time.Hour), Last: t0.Add(3 * time.Hour)})
	if back.Dismissed || !reflect.DeepEqual(back.Types, []delivery.ObservedType{"string", "number", "boolean"}) {
		t.Errorf("seen with a new type: %+v, want listed again", back)
	}
}
