package jsonx

import (
	"cmp"
	"math/big"
	"strings"
)

// EqualValue compares values decoded with UseNumber without rounding number
// tokens. Decimal exponents stay compact even when their powers are enormous.
func EqualValue(a, b any) bool {
	switch av := a.(type) {
	case Number:
		bv, ok := b.(Number)
		if !ok {
			return false
		}
		// Compare decimal significands and exponents without expanding powers:
		// a compact 1e1000000000 must not allocate a billion-digit integer.
		return canonicalNumber(string(av)) == canonicalNumber(string(bv))
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			other, found := bv[k]
			if !found || !EqualValue(v, other) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i, v := range av {
			if !EqualValue(v, bv[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

// CanonicalNumber returns a compact decimal token for a valid JSON number.
// Equivalent spellings share a token without floating-point rounding or
// expanding enormous exponents. Like EqualValue, it treats -0 as zero.
func CanonicalNumber(number Number) Number {
	return Number(canonicalNumber(string(number)))
}

func canonicalNumber(raw string) string {
	number := normalizeNumber(raw)
	if number.digits == "" {
		return "0"
	}
	sign := ""
	if number.negative {
		sign = "-"
	}
	return sign + number.digits + "e" + number.power.String()
}

type decimalNumber struct {
	negative bool
	digits   string
	power    *big.Int
}

func normalizeNumber(raw string) decimalNumber {
	mantissa, exponent, hasExponent := strings.Cut(strings.ToLower(raw), "e")
	power := new(big.Int)
	if hasExponent {
		power.SetString(exponent, 10)
	}
	negative := false
	if strings.HasPrefix(mantissa, "-") {
		negative = true
		mantissa = mantissa[1:]
	}
	whole, fraction, _ := strings.Cut(mantissa, ".")
	digits := strings.TrimLeft(whole+fraction, "0")
	if digits == "" {
		return decimalNumber{power: power}
	}
	trimmed := strings.TrimRight(digits, "0")
	power.Add(power, big.NewInt(int64(len(digits)-len(trimmed)-len(fraction))))
	return decimalNumber{negative: negative, digits: trimmed, power: power}
}

// CompareNumbers orders valid JSON number tokens by their exact decimal value.
// Magnitudes and mantissas stay compact: the cost follows token size rather
// than the size of the power represented by a token such as 1e1000000000.
func CompareNumbers(a, b Number) int {
	left, right := normalizeNumber(string(a)), normalizeNumber(string(b))
	if left.digits == "" || right.digits == "" {
		leftSign, rightSign := 1, 1
		if left.digits == "" {
			leftSign = 0
		} else if left.negative {
			leftSign = -1
		}
		if right.digits == "" {
			rightSign = 0
		} else if right.negative {
			rightSign = -1
		}
		return cmp.Compare(leftSign, rightSign)
	}
	if left.negative != right.negative {
		if left.negative {
			return -1
		}
		return 1
	}
	leftMagnitude := new(big.Int).Add(left.power, big.NewInt(int64(len(left.digits))))
	rightMagnitude := new(big.Int).Add(right.power, big.NewInt(int64(len(right.digits))))
	order := leftMagnitude.Cmp(rightMagnitude)
	if order == 0 {
		for i := range max(len(left.digits), len(right.digits)) {
			ld, rd := byte('0'), byte('0')
			if i < len(left.digits) {
				ld = left.digits[i]
			}
			if i < len(right.digits) {
				rd = right.digits[i]
			}
			order = cmp.Compare(ld, rd)
			if order != 0 {
				break
			}
		}
	}
	if left.negative {
		return -order
	}
	return order
}
