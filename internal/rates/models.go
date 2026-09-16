// Package rates owns the public market-reference-rate cache: fetching a daily
// provider snapshot, publishing it atomically and resolving a requested pair.
// It never participates in postings, so its retention policy cannot change
// historical accounting.
package rates

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Pivot is the snapshot base currency. Storing one pivot keeps a single daily
// request while an exact rational cross rate still resolves any supported pair.
const Pivot = "EUR"

// Source identifies the reference-rate provider adapter in stored snapshots.
const Source = "frankfurter"

// SourceVersion pins the upstream API version used for a snapshot.
const SourceVersion = "v2"

// DefaultRetentionDays keeps the snapshot day and the preceding 29 days.
const DefaultRetentionDays = 30

// BlendedFilter labels a snapshot that blends every upstream provider.
const BlendedFilter = "blended"

// Quote is one provider observation for base -> quote on an effective date.
type Quote struct {
	Base      string
	Quote     string
	Rate      string
	RateDate  string
	Providers []string
}

// Provider fetches the latest reference rates for one base currency.
type Provider interface {
	Latest(ctx context.Context, base string) ([]Quote, error)
}

// Status classifies a resolved reference rate.
type Status string

// Published statuses returned to clients.
const (
	StatusAvailable   Status = "available"
	StatusStale       Status = "stale"
	StatusUnavailable Status = "unavailable"
)

// Derived records how a pair was produced from the pivot-based snapshot.
type Derived string

// Derivations, from the provider's own quote to an exact rational cross rate.
const (
	DerivedDirect  Derived = "direct"
	DerivedInverse Derived = "inverse"
	DerivedCross   Derived = "cross"
)

// Reason explains an unavailable result without inventing a rate.
type Reason string

// Unavailable reasons.
const (
	ReasonNoSnapshot      Reason = "no_snapshot"
	ReasonPairUnavailable Reason = "pair_unavailable"
)

// Rate is the read model exposed by the HTTP boundary.
type Rate struct {
	Base               string
	Quote              string
	Status             Status
	Stale              bool
	Source             string
	ProviderFilter     string
	Pivot              string
	Display            string
	Numerator          string
	Denominator        string
	Derived            Derived
	RateDate           string
	SnapshotDate       string
	FetchedAt          string
	LatestSnapshotDate string
	Reason             Reason
}

// Result reports one daily synchronization attempt.
type Result struct {
	SnapshotDate string
	Published    bool
	Rates        int
}

// Filter canonicalizes a configured provider list into the value stored on a
// snapshot. An empty list means the provider's blended default.
func Filter(providers []string) string {
	if len(providers) == 0 {
		return BlendedFilter
	}
	keys := make([]string, 0, len(providers))
	seen := make(map[string]bool, len(providers))
	for _, provider := range providers {
		if key := strings.ToLower(strings.TrimSpace(provider)); key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return BlendedFilter
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// Clock returns the current time; tests inject a fixed implementation.
type Clock func() time.Time
