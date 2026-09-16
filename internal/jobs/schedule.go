package jobs

import (
	"time"

	"github.com/riverqueue/river"
)

// dailySchedule runs once per day at a fixed UTC time. It is a pure function of
// the current instant, so the UTC scheduling rule is unit-testable without a
// clock abstraction.
type dailySchedule struct {
	hour   int
	minute int
}

// Next returns the next fixed UTC instant strictly after the current time.
func (s dailySchedule) Next(current time.Time) time.Time {
	utc := current.UTC()
	next := time.Date(utc.Year(), utc.Month(), utc.Day(), s.hour, s.minute, 0, 0, time.UTC)
	if !next.After(utc) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// ratesSyncUTC is the pinned daily synchronization time, after European
// reference-rate publication, so one run captures the most complete day.
const (
	ratesSyncHour   = 17
	ratesSyncMinute = 10
)

// RatesSchedule returns the pinned daily reference-rate schedule.
func RatesSchedule() river.PeriodicSchedule {
	return dailySchedule{hour: ratesSyncHour, minute: ratesSyncMinute}
}
