// Package money represents amounts in minor units (céntimos, cents) so that no
// arithmetic ever touches floating point. In MySQL amounts are DECIMAL(12,2);
// on the wire they are decimal strings ("120.50").
package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Currency string

const (
	PEN Currency = "PEN"
	USD Currency = "USD"
)

// Supported lists the currencies the store prices and charges in.
var Supported = []Currency{PEN, USD}

var (
	ErrCurrency = errors.New("unsupported currency")
	ErrAmount   = errors.New("invalid amount")
)

func ParseCurrency(s string) (Currency, error) {
	c := Currency(strings.ToUpper(strings.TrimSpace(s)))
	for _, s := range Supported {
		if c == s {
			return c, nil
		}
	}
	return "", fmt.Errorf("%w: %q", ErrCurrency, s)
}

// Money is an amount in minor units (2 decimals for PEN and USD).
type Money struct {
	Minor    int64
	Currency Currency
}

func New(minor int64, c Currency) Money { return Money{Minor: minor, Currency: c} }

// Parse reads a decimal string with at most two decimals: "120", "120.5", "120.50".
func Parse(amount string, c Currency) (Money, error) {
	s := strings.TrimSpace(amount)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" || len(frac) > 2 || !digits(whole) || !digits(frac) {
		return Money{}, fmt.Errorf("%w: %q", ErrAmount, amount)
	}
	frac += strings.Repeat("0", 2-len(frac))
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w > 9_999_999_999 { // fits DECIMAL(12,2)
		return Money{}, fmt.Errorf("%w: %q", ErrAmount, amount)
	}
	f, _ := strconv.ParseInt(frac, 10, 64)
	minor := w*100 + f
	if neg {
		minor = -minor
	}
	return Money{Minor: minor, Currency: c}, nil
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Decimal formats the amount as a plain decimal string with two places.
func (m Money) Decimal() string {
	minor := m.Minor
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return fmt.Sprintf("%s%d.%02d", sign, minor/100, minor%100)
}

func (m Money) String() string { return m.Decimal() + " " + string(m.Currency) }

func (m Money) IsPositive() bool { return m.Minor > 0 }

func (m Money) Mul(qty int64) Money { return Money{Minor: m.Minor * qty, Currency: m.Currency} }

func (m Money) Add(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, fmt.Errorf("%w: %s + %s", ErrCurrency, m.Currency, o.Currency)
	}
	return Money{Minor: m.Minor + o.Minor, Currency: m.Currency}, nil
}
