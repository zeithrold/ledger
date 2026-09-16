package rates

import (
	"errors"
	"strings"
	"testing"

	"github.com/zeithrold/ledger/internal/problem"
)

func TestLegs(t *testing.T) {
	for _, tc := range []struct{ base, quote, first, second string }{
		{"EUR", "USD", "USD", ""},
		{"USD", "EUR", "USD", ""},
		{"USD", "CNY", "USD", "CNY"},
	} {
		first, second := legs(tc.base, tc.quote, Pivot)
		if first != tc.first || second != tc.second {
			t.Fatalf("%s/%s: %s %s", tc.base, tc.quote, first, second)
		}
	}
}

func TestResolveLegs(t *testing.T) {
	for _, tc := range []struct {
		name              string
		base, quote       string
		quotes            map[string]string
		display, num, den string
		derived           Derived
		used              []string
	}{
		{"direct", "EUR", "USD", map[string]string{"USD": "1.1"}, "1.1", "11", "10", DerivedDirect, []string{"USD"}},
		{"inverse", "USD", "EUR", map[string]string{"USD": "1.25"}, "0.8", "4", "5", DerivedInverse, []string{"USD"}},
		{"cross", "USD", "CNY", map[string]string{"USD": "1.1", "CNY": "7.7"}, "7", "7", "1", DerivedCross, []string{"USD", "CNY"}},
		{"cross repeating", "USD", "CNY", map[string]string{"USD": "3", "CNY": "7"}, "2.33333333333333333", "7", "3", DerivedCross, []string{"USD", "CNY"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			display, num, den, derived, used, ok := resolveLegs(tc.base, tc.quote, Pivot, tc.quotes)
			if !ok || display != tc.display || num != tc.num || den != tc.den || derived != tc.derived || strings.Join(used, ",") != strings.Join(tc.used, ",") {
				t.Fatalf("%s %s/%s %s => %s %s/%s %s %v", tc.name, tc.base, tc.quote, tc.display, display, num, den, derived, used)
			}
		})
	}
	for _, tc := range []struct {
		name        string
		base, quote string
		quotes      map[string]string
	}{
		{"missing leg", "USD", "CNY", map[string]string{"USD": "1.1"}},
		{"missing direct", "EUR", "USD", map[string]string{"CNY": "7.7"}},
		{"missing inverse", "USD", "EUR", map[string]string{"CNY": "7.7"}},
		{"invalid direct", "EUR", "USD", map[string]string{"USD": "1e2"}},
		{"invalid inverse", "USD", "EUR", map[string]string{"USD": "abc"}},
		{"zero inverse", "USD", "EUR", map[string]string{"USD": "0"}},
		{"zero leg", "USD", "CNY", map[string]string{"USD": "0", "CNY": "7"}},
		{"invalid decimal", "USD", "CNY", map[string]string{"USD": "1e2", "CNY": "7"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, _, _, ok := resolveLegs(tc.base, tc.quote, Pivot, tc.quotes); ok {
				t.Fatal("resolved an unusable snapshot")
			}
		})
	}
}

func TestSyncDailyRejectsInvalidInput(t *testing.T) {
	// A service without a database still validates the date and the provider, so
	// these branches are testable without PostgreSQL.
	service := New(nil, Options{})
	for _, date := range []string{"", "16/09/2026", "2026-2-01", "2026-02-30", "2026-09-16T00:00:00Z"} {
		if _, err := service.SyncDaily(t.Context(), date); err == nil {
			t.Fatalf("accepted snapshot date %q", date)
		}
	}
	if _, err := service.SyncDaily(t.Context(), "2026-09-16"); err == nil {
		t.Fatal("accepted a missing provider")
	}
	if service.providerFilter != BlendedFilter || service.retentionDays != DefaultRetentionDays || service.clock == nil {
		t.Fatalf("unexpected defaults: %+v", service)
	}
}

func TestFilter(t *testing.T) {
	for _, tc := range []struct {
		providers []string
		want      string
	}{
		{nil, BlendedFilter},
		{[]string{}, BlendedFilter},
		{[]string{" ", ""}, BlendedFilter},
		{[]string{"ECB"}, "ecb"},
		{[]string{"ECB", "boe"}, "boe,ecb"},
		{[]string{"ecb", "ECB", " ecb "}, "ecb"},
	} {
		if got := Filter(tc.providers); got != tc.want {
			t.Fatalf("%v: %s", tc.providers, got)
		}
	}
}

func TestNormalizeQuotes(t *testing.T) {
	rows, err := normalizeQuotes([]Quote{
		{Base: "EUR", Quote: "EUR", Rate: "1.0", RateDate: "2026-09-16"},
		{Base: "EUR", Quote: "USD", Rate: "1.1", RateDate: "2026-09-16", Providers: []string{"ECB", "ecb", " BOE "}},
		{Base: "EUR", Quote: "USD", Rate: "9.9", RateDate: "2026-09-15"},
		{Base: "EUR", Quote: "CNY", Rate: "7.7", RateDate: "2026-09-15"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].quote != "USD" || rows[0].rate != "1.1" || rows[0].rateDate.Format("2006-01-02") != "2026-09-16" {
		t.Fatalf("rows %+v", rows)
	}
	if strings.Join(rows[0].providers, ",") != "boe,ecb" {
		t.Fatalf("providers %v", rows[0].providers)
	}
	if rows[1].rate != "7.7" {
		t.Fatalf("rows %+v", rows)
	}
	providers := make([]string, 0, maxProviders+5)
	for i := 0; i < maxProviders+5; i++ {
		providers = append(providers, string(rune('a'+i%26))+string(rune('0'+i/26)))
	}
	capped, err := normalizeQuotes([]Quote{{Base: "EUR", Quote: "USD", Rate: "1", RateDate: "2026-09-16", Providers: providers}})
	if err != nil || len(capped[0].providers) != maxProviders {
		t.Fatalf("provider cap %d %v", len(capped[0].providers), err)
	}
}

func TestNormalizeQuotesRejectsInvalidRows(t *testing.T) {
	tooMany := make([]Quote, maxQuotes+1)
	for i := range tooMany {
		tooMany[i] = Quote{Base: "EUR", Quote: "USD", Rate: "1", RateDate: "2026-09-16"}
	}
	for name, quotes := range map[string][]Quote{
		"empty":          nil,
		"base mismatch":  {{Base: "USD", Quote: "CNY", Rate: "1", RateDate: "2026-09-16"}},
		"pivot quote":    {{Base: "EUR", Quote: "EUR", Rate: "1", RateDate: "2026-09-16"}},
		"lowercase":      {{Base: "EUR", Quote: "usd", Rate: "1", RateDate: "2026-09-16"}},
		"long code":      {{Base: "EUR", Quote: "USDD", Rate: "1", RateDate: "2026-09-16"}},
		"zero rate":      {{Base: "EUR", Quote: "USD", Rate: "0", RateDate: "2026-09-16"}},
		"negative rate":  {{Base: "EUR", Quote: "USD", Rate: "-1", RateDate: "2026-09-16"}},
		"huge rate":      {{Base: "EUR", Quote: "USD", Rate: "1000000000000000000", RateDate: "2026-09-16"}},
		"exponent rate":  {{Base: "EUR", Quote: "USD", Rate: "1e2", RateDate: "2026-09-16"}},
		"invalid date":   {{Base: "EUR", Quote: "USD", Rate: "1", RateDate: "16/09/2026"}},
		"impossible day": {{Base: "EUR", Quote: "USD", Rate: "1", RateDate: "2026-02-30"}},
		"too many":       tooMany,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeQuotes(quotes); err == nil {
				t.Fatal("accepted invalid provider rows")
			}
		})
	}
}

func TestValidatePair(t *testing.T) {
	if err := validatePair("USD", "CNY"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ base, quote string }{
		{"usd", "CNY"}, {"USD", "cny"}, {"US", "CNY"}, {"USD", "CNYY"}, {"USD", "USD"}, {"ABC", "CNY"}, {"USD", "ABC"}, {"", "CNY"},
	} {
		err := validatePair(tc.base, tc.quote)
		var p *problem.Error
		if !errors.As(err, &p) || p.Kind != problem.InvalidRequest || len(p.Fields) != 1 {
			t.Fatalf("%s/%s: %v", tc.base, tc.quote, err)
		}
	}
}

func TestEarliest(t *testing.T) {
	dates := map[string]string{"USD": "2026-09-16", "CNY": "2026-09-14"}
	if got := earliest([]string{"USD", "CNY"}, dates); got != "2026-09-14" {
		t.Fatal(got)
	}
	if got := earliest([]string{"USD", "GBP"}, dates); got != "2026-09-16" {
		t.Fatal(got)
	}
	if got := earliest([]string{"GBP"}, dates); got != "" {
		t.Fatal(got)
	}
}
