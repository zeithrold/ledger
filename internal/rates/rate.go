package rates

import (
	"math/big"

	"github.com/zeithrold/ledger/internal/money"
)

// displayDigits is the maximum number of significant digits in a derived
// display rate, matching the applied-rate display contract.
const displayDigits = 18

// legs returns the pivot quote codes a pair needs, or an empty second leg.
func legs(base, quote, pivot string) (string, string) {
	switch {
	case base == pivot:
		return quote, ""
	case quote == pivot:
		return base, ""
	default:
		return base, quote
	}
}

// resolveLegs produces the exact base -> quote rate from pivot quotes, where
// each leg is the value of one pivot unit in that currency. The returned legs
// are the codes actually used, so callers can report the effective date and
// provenance of exactly what was consumed.
func resolveLegs(base, quote, pivot string, quotes map[string]string) (display, numerator, denominator string, derived Derived, used []string, ok bool) {
	switch {
	case base == pivot:
		raw, found := quotes[quote]
		if !found {
			return "", "", "", "", nil, false
		}
		value, err := money.ParseDecimal(raw)
		if err != nil {
			return "", "", "", "", nil, false
		}
		return raw, value.Num().String(), value.Denom().String(), DerivedDirect, []string{quote}, true
	case quote == pivot:
		raw, found := quotes[base]
		if !found {
			return "", "", "", "", nil, false
		}
		value, err := money.ParseDecimal(raw)
		if err != nil || value.Sign() == 0 {
			return "", "", "", "", nil, false
		}
		inverse := new(big.Rat).Inv(value)
		return money.SignificantDigits(inverse, displayDigits), inverse.Num().String(), inverse.Denom().String(), DerivedInverse, []string{base}, true
	default:
		from, foundFrom := quotes[base]
		to, foundTo := quotes[quote]
		if !foundFrom || !foundTo {
			return "", "", "", "", nil, false
		}
		fromValue, fromErr := money.ParseDecimal(from)
		toValue, toErr := money.ParseDecimal(to)
		if fromErr != nil || toErr != nil || fromValue.Sign() == 0 {
			return "", "", "", "", nil, false
		}
		cross := new(big.Rat).Quo(toValue, fromValue)
		return money.SignificantDigits(cross, displayDigits), cross.Num().String(), cross.Denom().String(), DerivedCross, []string{base, quote}, true
	}
}
