package accounting

import (
	"reflect"
	"testing"

	"github.com/zeithrold/ledger/internal/money"
)

// TestMoneyFacadeIsThePureKernel keeps the compatibility surface honest: the
// accounting package must expose the exact kernel types rather than a copy that
// could drift from the covered, mutation-tested implementation.
func TestMoneyFacadeIsThePureKernel(t *testing.T) {
	if got := reflect.TypeOf(Money{}).PkgPath(); got != "github.com/zeithrold/ledger/internal/money" {
		t.Fatalf("Money is defined in %q", got)
	}
	if got := reflect.TypeOf(Currency{}).PkgPath(); got != "github.com/zeithrold/ledger/internal/money" {
		t.Fatalf("Currency is defined in %q", got)
	}
}

func TestCurrencyCatalogAndPrecision(t *testing.T) {
	catalog := Currencies()
	if len(catalog) != 148 {
		t.Fatalf("catalog has %d currencies", len(catalog))
	}
	seen := map[string]bool{}
	for _, currency := range catalog {
		if currency.Code == "" || currency.MinorUnits < 0 || currency.MinorUnits > 4 {
			t.Fatalf("invalid entry %+v", currency)
		}
		if seen[currency.Code] {
			t.Fatalf("duplicate currency %s", currency.Code)
		}
		seen[currency.Code] = true
		units, ok := currencyUnits[currency.Code]
		if !ok || units != currency.MinorUnits {
			t.Fatalf("lookup disagrees for %s: %d %v", currency.Code, units, ok)
		}
	}
	if len(currencyUnits) != len(catalog) {
		t.Fatalf("lookup has %d entries for %d currencies", len(currencyUnits), len(catalog))
	}
	for code, scale := range map[string]int{"USD": 2, "JPY": 0, "KWD": 3} {
		if currencyUnits[code] != scale {
			t.Fatalf("%s scale %d", code, currencyUnits[code])
		}
	}
}

func TestParseMoneyDelegatesAndRejectsImplicitRounding(t *testing.T) {
	for _, tc := range []struct {
		value, currency string
		want            string
		invalid         bool
	}{
		{"12.34", "USD", "12.34", false},
		{"-0.05", "USD", "-0.05", false},
		{"0", "JPY", "0", false},
		{"1.5", "JPY", "", true},
		{"1.234", "KWD", "1.234", false},
		{"1.2345", "KWD", "", true},
		{"1e3", "USD", "", true},
		{"12.34", "ZZZ", "", true},
	} {
		got, err := ParseMoney(tc.value, tc.currency)
		want, wantErr := money.ParseMoney(tc.value, tc.currency)
		if (err != nil) != tc.invalid || (wantErr != nil) != (err != nil) {
			t.Fatalf("%s/%s: %v want %v", tc.value, tc.currency, err, wantErr)
		}
		if err != nil {
			continue
		}
		if got.String() != tc.want || got.String() != want.String() || got.Cmp(want) != 0 {
			t.Fatalf("%s/%s: %s want %s", tc.value, tc.currency, got.String(), tc.want)
		}
	}
}

func TestRatioMatchesTheKernel(t *testing.T) {
	source, err := ParseMoney("100.00", "USD")
	if err != nil {
		t.Fatal(err)
	}
	destination, err := ParseMoney("92.50", "EUR")
	if err != nil {
		t.Fatal(err)
	}
	num, denom, display := Ratio(source, destination)
	wantNum, wantDenom, wantDisplay := money.Ratio(source, destination)
	if num != wantNum || denom != wantDenom || display != wantDisplay {
		t.Fatalf("%s/%s/%s want %s/%s/%s", num, denom, display, wantNum, wantDenom, wantDisplay)
	}
	if num != "37" || denom != "40" || display != "0.925" {
		t.Fatalf("unexpected ratio %s/%s %s", num, denom, display)
	}
}
