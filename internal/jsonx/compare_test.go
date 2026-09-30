package jsonx

import (
	"math/big"
	"strings"
	"testing"
)

func TestCompareNumbersAgreesWithExactRationals(t *testing.T) {
	t.Parallel()
	tokens := []string{"-123e9", "-12e9", "-1.1999999999999999999", "-1.20", "-0.001", "-0e999", "0", "1e-9", "0.1000000000000000001", "0.1000000000000000002", "1", "1.00", "1e0", "12e9", "123e8", "9007199254740992", "9007199254740993"}
	for _, a := range tokens {
		left, ok := new(big.Rat).SetString(a)
		if !ok {
			t.Fatal(a)
		}
		for _, b := range tokens {
			right, ok := new(big.Rat).SetString(b)
			if !ok {
				t.Fatal(b)
			}
			got := CompareNumbers(Number(a), Number(b))
			if want := left.Cmp(right); got != want {
				t.Fatalf("%s compared with %s: %d; want %d", a, b, got, want)
			}
			if (got == 0) != EqualValue(Number(a), Number(b)) {
				t.Fatalf("ordering/equality disagree: %s / %s", a, b)
			}
		}
	}
}

func TestCompareNumbersKeepsArbitraryExponentsCompact(t *testing.T) {
	t.Parallel()
	exponent := strings.Repeat("9", 10000)
	for _, pair := range []struct{ a, b string }{
		{"1e" + exponent, "2e" + exponent},
		{"1e-" + exponent, "2e-" + exponent},
		{"-2e" + exponent, "-1e" + exponent},
	} {
		if got := CompareNumbers(Number(pair.a), Number(pair.b)); got != -1 {
			t.Fatalf("compact exponent comparison=%d; want -1", got)
		}
	}
}
