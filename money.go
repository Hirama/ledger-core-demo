package ledger

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
)

const WindowDays = 6
const dailyRateDenominator = 2500 // 1/2500 = 0.04%.

var decimalPattern = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?$`)

func precision(currency string) (int, bool) {
	switch currency {
	case "AED":
		return 2, true
	case "BHD":
		return 3, true
	}
	return 0, false
}

// roundRational rounds exactly to nearest integer, with midpoint ties to even.
func roundRational(number *big.Rat) *big.Int {
	numerator := new(big.Int).Abs(number.Num())
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, number.Denom(), remainder)
	twice := new(big.Int).Lsh(remainder, 1)
	comparison := twice.Cmp(number.Denom())
	if comparison > 0 || (comparison == 0 && quotient.Bit(0) == 1) {
		quotient.Add(quotient, big.NewInt(1))
	}
	if number.Sign() < 0 {
		quotient.Neg(quotient)
	}
	return quotient
}

// ParseMinor accepts fixed decimal strings, never floats, and checks int64 bounds.
func ParseMinor(amount, currency string) (int64, error) {
	digits, ok := precision(currency)
	if !ok {
		return 0, errors.New("UNSUPPORTED_CURRENCY")
	}
	if !decimalPattern.MatchString(amount) {
		return 0, errors.New("INVALID_DECIMAL")
	}
	number, ok := new(big.Rat).SetString(amount)
	if !ok {
		return 0, errors.New("INVALID_DECIMAL")
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	rounded := roundRational(number.Mul(number, new(big.Rat).SetInt(scale)))
	if !rounded.IsInt64() {
		return 0, errors.New("AMOUNT_OVERFLOW")
	}
	return rounded.Int64(), nil
}

func FormatMoney(amount int64, currency string) string {
	digits, ok := precision(currency)
	if !ok {
		panic("unsupported currency")
	}
	scale := uint64(100)
	if digits == 3 {
		scale = 1000
	}
	magnitude := uint64(amount)
	sign := ""
	if amount < 0 {
		sign = "-"
		magnitude = uint64(-(amount + 1)) + 1
	}
	return fmt.Sprintf("%s%d.%0*d", sign, magnitude/scale, digits, magnitude%scale)
}

// SplitMinor conserves the total; earlier parts receive indivisible units.
func SplitMinor(total int64, count int) ([]int64, error) {
	if total < 0 || count <= 0 {
		return nil, errors.New("INVALID_SPLIT")
	}
	quotient, remainder := total/int64(count), total%int64(count)
	parts := make([]int64, count)
	for i := range parts {
		parts[i] = quotient
		if int64(i) < remainder {
			parts[i]++
		}
	}
	return parts, nil
}

func checkedAdd(a, b int64) (int64, error) {
	sum := new(big.Int).Add(big.NewInt(a), big.NewInt(b))
	if !sum.IsInt64() {
		return 0, errors.New("BALANCE_OVERFLOW")
	}
	return sum.Int64(), nil
}

func checkedSubtract(a, b int64) (int64, error) {
	difference := new(big.Int).Sub(big.NewInt(a), big.NewInt(b))
	if !difference.IsInt64() {
		return 0, errors.New("AVAILABLE_OVERFLOW")
	}
	return difference.Int64(), nil
}
