package accounting

import (
	"math/big"
	"strings"
	"testing"
)

func TestMoneyPrecision(t *testing.T) {
	for _, tc := range []struct{ value, currency, want string }{
		{"1", "USD", "1.00"},
		{"1.20", "USD", "1.20"},
		{"-0.00", "USD", "0.00"},
		{"999999999999999999.99", "USD", "999999999999999999.99"},
		{"-42", "JPY", "-42"},
		{"0.001", "KWD", "0.001"},
	} {
		m, err := ParseMoney(tc.value, tc.currency)
		if err != nil || m.String() != tc.want {
			t.Fatalf("%s %s: %v %v", tc.value, tc.currency, m, err)
		}
	}
	for _, tc := range []struct{ value, currency string }{
		{"1.001", "USD"},
		{"1.0", "JPY"},
		{"1000000000000000000", "USD"},
		{"NaN", "USD"},
		{"Infinity", "USD"},
		{"1e2", "USD"},
		{"+1", "USD"},
		{" 1", "USD"},
		{"01", "USD"},
		{".1", "USD"},
		{"1.", "USD"},
		{"1/2", "USD"},
		{"1", "XXX"},
		{"1", "ZZZ"},
	} {
		if _, err := ParseMoney(tc.value, tc.currency); err == nil {
			t.Fatalf("accepted %q %s", tc.value, tc.currency)
		}
	}
	// Synthetic precision tests do not add an unsupported code to the API catalog.
	currencyUnits["TST"] = 4
	t.Cleanup(func() { delete(currencyUnits, "TST") })
	for _, value := range []string{"1.2345", "-999999999999999999.9999"} {
		parsed, err := ParseMoney(value, "TST")
		if err != nil || parsed.String() != value {
			t.Fatalf("four-place amount %s: %v", value, err)
		}
	}
	if _, err := ParseMoney("1.23456", "TST"); err == nil {
		t.Fatal("accepted excess four-place precision")
	}
	// The pure representation also retains a four-place negative quantity.
	if got := (Money{big.NewInt(-12345), 4}).String(); got != "-1.2345" {
		t.Fatal(got)
	}
	if len(Currencies()) != 148 {
		t.Fatal("currency catalog drift")
	}
}

func TestExactRatioAndHalfEven(t *testing.T) {
	a, err := ParseMoney("100", "USD")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseMoney("720", "CNY")
	if err != nil {
		t.Fatal(err)
	}
	n, d, display := Ratio(a, b)
	if n != "36" || d != "5" || display != "7.2" {
		t.Fatalf("%s/%s %s", n, d, display)
	}
	for _, tc := range []struct {
		n, d   int64
		digits int
		want   string
	}{
		{1, 3, 18, "0.333333333333333333"},
		{125, 100, 2, "1.2"},
		{135, 100, 2, "1.4"},
		{999, 100, 2, "10"},
		{1, 1000000, 2, "0.000001"},
		{-125, 100, 2, "-1.2"},
		{0, 1, 18, "0"},
		{12345, 1, 2, "12000"},
		{9999, 1, 2, "10000"},
		{999, 10000, 2, "0.1"},
		{1234, 98765, 2, "0.012"},
		{9, 1, 1, "9"},
		{1, 1, 1, "1"},
		{1, 10, 1, "0.1"},
	} {
		if got := significant(big.NewRat(tc.n, tc.d), tc.digits); got != tc.want {
			t.Fatalf("%+v: %s", tc, got)
		}
	}
}

func TestBalanced(t *testing.T) {
	for _, tc := range []struct {
		values []string
		want   bool
	}{
		{nil, false},
		{[]string{"0"}, false},
		{[]string{"-100.00", "100"}, true},
		{[]string{"1.10", "-1.00", "-0.10"}, true},
		{[]string{"-100", "720"}, false},
		{[]string{"NaN", "0"}, false},
		{[]string{"1/2", "-0.5"}, false},
		{[]string{"1." + strings.Repeat("0", 62), "-1"}, true},
		{[]string{"1." + strings.Repeat("0", 63), "-1"}, false},
	} {
		if got := Balanced(tc.values); got != tc.want {
			t.Fatalf("%v: %v", tc.values, got)
		}
	}
}

func FuzzMoneyRoundTrip(f *testing.F) {
	for _, s := range []string{"0", "-0.00", "0.01", "999999999999999999.99", "1e2", "NaN"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		m, err := ParseMoney(value, "USD")
		if err != nil {
			return
		}
		n, err := ParseMoney(m.String(), "USD")
		if err != nil || m.units.Cmp(n.units) != 0 {
			t.Fatal("money round trip changed value")
		}
		if !Balanced([]string{m.String(), (Money{new(big.Int).Neg(m.units), m.scale}).String()}) {
			t.Fatal("inverse does not balance")
		}
	})
}

func FuzzExactRatio(f *testing.F) {
	f.Add(uint64(100), uint64(720))
	f.Add(uint64(3), uint64(1))
	f.Fuzz(func(t *testing.T, x, y uint64) {
		if x == 0 || y == 0 {
			return
		}
		a := Money{new(big.Int).SetUint64(x), 2}
		b := Money{new(big.Int).SetUint64(y), 3}
		n, d, _ := Ratio(a, b)
		r, ok := new(big.Rat).SetString(n + "/" + d)
		if !ok || new(big.Rat).Mul(a.rat(), r).Cmp(b.rat()) != 0 {
			t.Fatal("exact ratio lost precision")
		}
	})
}

func FuzzBalance(f *testing.F) {
	f.Add("1.20", "-1.20")
	f.Add("1/2", "-0.5")
	f.Fuzz(func(t *testing.T, a, b string) {
		values := []string{a, b}
		want := true
		total := new(big.Rat)
		for _, s := range values {
			if len(s) > 64 || !amountPattern.MatchString(s) {
				want = false
				break
			}
			r, ok := new(big.Rat).SetString(s)
			if !ok {
				want = false
				break
			}
			total.Add(total, r)
		}
		want = want && total.Sign() == 0
		if Balanced(values) != want {
			t.Fatal("balance disagrees with exact sum")
		}
	})
}
