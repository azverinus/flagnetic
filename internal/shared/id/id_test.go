package id_test

import (
	"encoding/json"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/azverinus/flagnetic/internal/shared/clock"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

type flag struct{}

type flagID = id.Of[flag]

// RFC 9562: version 7 in the high nibble of byte 6, variant 0b10 in the top
// bits of byte 8, written as lowercase hyphenated hex.
var v7 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewIsAWellFormedUUIDv7OfItsTime(t *testing.T) {
	// 2016-07-30T22:37:44.193Z is 0x01563df4bb81 milliseconds since the epoch.
	at := time.UnixMilli(1469918264193)
	for name, src := range map[string]*id.Source{
		"random":        id.NewSource(clock.NewFake(at)),
		"deterministic": id.NewDeterministicSource(clock.NewFake(at)),
	} {
		t.Run(name, func(t *testing.T) {
			got := id.New[flag](src)
			if !v7.MatchString(got.String()) {
				t.Errorf("New = %s, not a canonical UUIDv7", got)
			}
			if got.String()[:13] != "01563df4-bb81" {
				t.Errorf("New = %s, want it to start with the time 01563df4-bb81", got)
			}
			if !got.Time().Equal(at) {
				t.Errorf("Time = %v, want %v", got.Time(), at)
			}
		})
	}
}

func TestNewIsStrictlyIncreasingWithinOneMillisecond(t *testing.T) {
	c := clock.NewFake(time.UnixMilli(1469918264193))
	src := id.NewSource(c)
	prev := id.New[flag](src)
	for range 1000 {
		next := id.New[flag](src)
		if next.String() <= prev.String() {
			t.Fatalf("%s after %s is not increasing", next, prev)
		}
		prev = next
	}
	c.Advance(-time.Minute)
	if earlier := id.New[flag](src); earlier.String() <= prev.String() {
		t.Errorf("an identifier minted after the clock stepped back went backwards: %s after %s", earlier, prev)
	}
}

func TestNewIsStrictlyIncreasingAcrossGoroutines(t *testing.T) {
	src := id.NewSource(clock.NewFake(time.UnixMilli(1469918264193)))
	var mu sync.Mutex
	seen := make(map[flagID]bool)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				got := id.New[flag](src)
				mu.Lock()
				seen[got] = true
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if len(seen) != 1600 {
		t.Errorf("minted %d distinct identifiers out of 1600", len(seen))
	}
}

func TestDeterministicSourceRepeatsItselfAndStaysOrdered(t *testing.T) {
	mint := func() []string {
		c := clock.NewFake(time.UnixMilli(1469918264193))
		src := id.NewDeterministicSource(c)
		var out []string
		for range 3 {
			out = append(out, id.New[flag](src).String())
		}
		c.Advance(time.Millisecond)
		out = append(out, id.New[flag](src).String())
		return out
	}
	first, second := mint(), mint()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("two deterministic sources disagree at %d: %s and %s", i, first[i], second[i])
		}
		if !v7.MatchString(first[i]) {
			t.Errorf("%s is not a canonical UUIDv7", first[i])
		}
		if i > 0 && first[i] <= first[i-1] {
			t.Errorf("%s after %s is not increasing", first[i], first[i-1])
		}
	}
	if first[0] != "01563df4-bb81-7000-8000-000000000000" {
		t.Errorf("first deterministic identifier = %s", first[0])
	}
}

func TestParseRoundTripsTheCanonicalForm(t *testing.T) {
	const s = "01563df4-bb81-7abc-9def-0123456789ab"
	got, err := id.Parse[flag](s)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.String() != s {
		t.Errorf("String = %s, want %s", got, s)
	}
	upper, err := id.Parse[flag]("01563DF4-BB81-7ABC-9DEF-0123456789AB")
	if err != nil || upper != got {
		t.Errorf("Parse of the upper-case form = %s, %v; want %s", upper, err, got)
	}
}

func TestParseRefusesMalformedInput(t *testing.T) {
	for _, s := range []string{
		"",
		"01563df4bb817abc9def0123456789ab",
		"01563df4-bb81-7abc-9def-0123456789a",
		"01563df4-bb81-7abc-9def-0123456789abc",
		"01563df4-bb81-7abc-9def_0123456789ab",
		"01563df4-bb81-7abc-9def-0123456789ag",
		"{01563df4-bb81-7abc-9def-0123456789ab}",
	} {
		if _, err := id.Parse[flag](s); err == nil {
			t.Errorf("Parse(%q) succeeded", s)
		}
	}
}

func TestZero(t *testing.T) {
	var zero flagID
	if !zero.IsZero() {
		t.Error("the zero value is not zero")
	}
	if id.New[flag](id.NewSource(clock.System{})).IsZero() {
		t.Error("a minted identifier is zero")
	}
}

func TestTextMarshallingRoundTrips(t *testing.T) {
	type body struct {
		ID flagID `json:"id"`
	}
	want := id.MustParse[flag]("01563df4-bb81-7abc-9def-0123456789ab")
	b, err := json.Marshal(body{ID: want})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"id":"01563df4-bb81-7abc-9def-0123456789ab"}` {
		t.Errorf("Marshal = %s", b)
	}
	var got body
	if err := json.Unmarshal(b, &got); err != nil || got.ID != want {
		t.Errorf("Unmarshal = %s, %v; want %s", got.ID, err, want)
	}
	if err := json.Unmarshal([]byte(`{"id":"nope"}`), &got); err == nil {
		t.Error("Unmarshal of a malformed identifier succeeded")
	}
}

func TestBytesRoundTrip(t *testing.T) {
	want := id.MustParse[flag]("01563df4-bb81-7abc-9def-0123456789ab")
	if got := id.FromBytes[flag](want.Bytes()); got != want {
		t.Errorf("FromBytes(Bytes()) = %s, want %s", got, want)
	}
}

func TestMustParsePanicsOnMalformedInput(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustParse did not panic")
		}
	}()
	id.MustParse[flag]("nope")
}
