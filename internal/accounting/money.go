// Package accounting owns authorized atomic personal ledger operations.
package accounting

import "github.com/zeithrold/ledger/internal/money"

// Currency is shared with the pure monetary kernel.
type Currency = money.Currency

// Money is an immutable exact amount.
type Money = money.Money

// Currencies returns the pinned reference catalog.
func Currencies() []Currency { return money.Currencies() }

// ParseMoney parses an exact amount using the pinned currency precision.
func ParseMoney(value, currency string) (Money, error) { return money.ParseMoney(value, currency) }

// Ratio returns the exact conversion rate and rounded display representation.
func Ratio(source, destination Money) (string, string, string) {
	return money.Ratio(source, destination)
}

var currencyUnits = func() map[string]int {
	units := make(map[string]int)
	for _, currency := range Currencies() {
		units[currency.Code] = currency.MinorUnits
	}
	return units
}()
