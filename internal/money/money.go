// Package money owns exact amounts without transport or persistence dependencies.
package money

import (
	_ "embed"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strings"
)

// Currency is a pinned CLDR 48 legal-tender currency supported by both clients.
type Currency struct {
	Code       string `json:"code"`
	MinorUnits int    `json:"minor_units"`
}

//go:embed currencies.json
var currencyJSON []byte

// Currencies returns a fresh copy of the shared reference catalog.
func Currencies() []Currency {
	var result []Currency
	if err := json.Unmarshal(currencyJSON, &result); err != nil {
		panic("invalid embedded currency catalog")
	}
	return result
}

var currencyUnits = func() map[string]int {
	result := map[string]int{}
	for _, c := range Currencies() {
		result[c.Code] = c.MinorUnits
	}
	return result
}()

var amountPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// Money stores signed integer minor units. Its methods never mutate the value.
type Money struct {
	units *big.Int
	scale int
}

// Sign reports whether the amount is negative, zero, or positive.
func (m Money) Sign() int { return m.units.Sign() }

// Cmp compares major-unit quantities exactly, including differing precisions.
func (m Money) Cmp(other Money) int { return m.rat().Cmp(other.rat()) }

// Add returns an amount at the same precision. Callers must use one currency.
func (m Money) Add(other Money) Money {
	if m.scale != other.scale {
		panic("cannot add amounts with different precision")
	}
	return Money{new(big.Int).Add(m.units, other.units), m.scale}
}

// Neg returns the exact additive inverse without mutating the receiver.
func (m Money) Neg() Money { return Money{new(big.Int).Neg(m.units), m.scale} }

// Rational returns an independent exact major-unit value.
func (m Money) Rational() *big.Rat { return m.rat() }

// ParseMoney rejects implicit rounding, exponents, non-finite and out-of-range amounts.
func ParseMoney(value, currency string) (Money, error) {
	scale, ok := currencyUnits[currency]
	if !ok || len(value) > 24 || !amountPattern.MatchString(value) {
		return Money{}, errors.New("invalid amount or currency")
	}
	parts := strings.Split(strings.TrimPrefix(value, "-"), ".")
	if len(parts[0]) > 18 {
		return Money{}, errors.New("amount must be less than 10^18")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > scale {
		return Money{}, errors.New("amount exceeds currency precision")
	}
	n, ok := new(big.Int).SetString(parts[0]+fraction+strings.Repeat("0", scale-len(fraction)), 10)
	if !ok {
		return Money{}, errors.New("invalid amount")
	}
	if strings.HasPrefix(value, "-") {
		n.Neg(n)
	}
	return Money{n, scale}, nil
}

// String renders a canonical decimal without floating point conversion.
func (m Money) String() string {
	sign := ""
	if m.units.Sign() < 0 {
		sign = "-"
	}
	s := new(big.Int).Abs(m.units).String()
	if m.scale == 0 {
		return sign + s
	}
	if len(s) <= m.scale {
		s = strings.Repeat("0", m.scale-len(s)+1) + s
	}
	return sign + s[:len(s)-m.scale] + "." + s[len(s)-m.scale:]
}

func (m Money) rat() *big.Rat {
	return new(big.Rat).SetFrac(m.units, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(m.scale)), nil))
}

// ParseDecimal parses a canonical decimal string into an exact rational.
// It rejects exponents, a leading plus sign, redundant leading zeros and
// unreasonably long values without any binary floating point conversion.
func ParseDecimal(value string) (*big.Rat, error) {
	if len(value) == 0 || len(value) > 40 || !amountPattern.MatchString(value) {
		return nil, errors.New("invalid decimal")
	}
	negative := strings.HasPrefix(value, "-")
	parts := strings.SplitN(strings.TrimPrefix(value, "-"), ".", 2)
	digits, scale := parts[0], 0
	if len(parts) == 2 {
		digits += parts[1]
		scale = len(parts[1])
	}
	units, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, errors.New("invalid decimal")
	}
	if negative {
		units.Neg(units)
	}
	return new(big.Rat).SetFrac(units, new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)), nil
}

// Ratio returns the exact major-unit destination/source rate as integer strings.
func Ratio(source, destination Money) (string, string, string) {
	r := new(big.Rat).Quo(destination.rat(), source.rat())
	return r.Num().String(), r.Denom().String(), significant(r, 18)
}

// SignificantDigits renders an exact rational as a decimal with at most the
// given number of significant digits, using decimal half-even rounding with no
// binary intermediate. It panics when digits is not positive.
func SignificantDigits(r *big.Rat, digits int) string {
	if digits < 1 {
		panic("money: significant digits must be positive")
	}
	return significant(r, digits)
}

// significant uses decimal half-even rounding, with no binary intermediate.
func significant(r *big.Rat, digits int) string {
	if r.Sign() == 0 {
		return "0"
	}
	n := new(big.Int).Abs(r.Num())
	d := new(big.Int).Set(r.Denom())
	exponent := len(n.String()) - len(d.String())
	pow := func(e int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(e)), nil) }
	if exponent >= 0 {
		if n.Cmp(new(big.Int).Mul(d, pow(exponent))) < 0 {
			exponent--
		}
	} else if new(big.Int).Mul(n, pow(-exponent)).Cmp(d) < 0 {
		exponent--
	}
	scale := digits - 1 - exponent
	if scale >= 0 {
		n.Mul(n, pow(scale))
	} else {
		d.Mul(d, pow(-scale))
	}
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(n, d, rem)
	cmp := new(big.Int).Lsh(rem, 1).Cmp(d)
	if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
		q.Add(q, big.NewInt(1))
	}
	if r.Sign() < 0 {
		q.Neg(q)
	}
	if scale < 0 {
		return new(big.Int).Mul(q, pow(-scale)).String()
	}
	s := (Money{q, scale}).String()
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// Balanced compares exact quantities in one common valuation currency.
func Balanced(values []string) bool {
	if len(values) < 2 {
		return false
	}
	total := new(big.Rat)
	for _, value := range values {
		if len(value) > 64 || !amountPattern.MatchString(value) {
			return false
		}
		n, ok := new(big.Rat).SetString(value)
		if !ok {
			return false
		}
		total.Add(total, n)
	}
	return total.Sign() == 0
}
