package jobs

import (
	"encoding/json"
	"testing"
	"time"
)

func TestRatesSchedule(t *testing.T) {
	schedule := RatesSchedule()
	for _, tc := range []struct{ now, want string }{
		{"2026-09-16T00:00:00Z", "2026-09-16T17:10:00Z"},
		{"2026-09-16T17:09:59Z", "2026-09-16T17:10:00Z"},
		{"2026-09-16T17:10:00Z", "2026-09-17T17:10:00Z"},
		{"2026-09-16T23:30:00Z", "2026-09-17T17:10:00Z"},
	} {
		now, err := time.Parse(time.RFC3339, tc.now)
		if err != nil {
			t.Fatal(err)
		}
		next := schedule.Next(now)
		if got := next.UTC().Format(time.RFC3339); got != tc.want {
			t.Fatalf("from %s: %s", tc.now, got)
		}
		if next.Location() != time.UTC {
			t.Fatalf("from %s: location %s", tc.now, next.Location())
		}
	}
	// The schedule follows the UTC calendar day, not the caller's zone: 01:00 in
	// UTC+8 is still 17:00 UTC of the previous day.
	zone := time.FixedZone("UTC+8", 8*3600)
	now := time.Date(2026, 9, 16, 1, 0, 0, 0, zone)
	want := time.Date(2026, 9, 15, 17, 10, 0, 0, time.UTC)
	if got := schedule.Next(now); !got.Equal(want) {
		t.Fatalf("non-UTC instant scheduled %s", got)
	}
	if schedule.Next(now).Before(now) {
		t.Fatal("scheduled a time before the current instant")
	}
}

func TestFetchRatesArgs(t *testing.T) {
	if kind := (FetchRatesArgs{}).Kind(); kind != "rates_fetch_daily" {
		t.Fatalf("kind %s", kind)
	}
	encoded, err := json.Marshal(FetchRatesArgs{})
	if err != nil || string(encoded) != "{}" {
		t.Fatalf("empty args encode as %s (%v)", encoded, err)
	}
	encoded, err = json.Marshal(FetchRatesArgs{ScheduledFor: "2026-09-16"})
	if err != nil || string(encoded) != `{"scheduled_for":"2026-09-16"}` {
		t.Fatalf("args encode as %s (%v)", encoded, err)
	}
}
