package money

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := map[string]int64{"120": 12000, "120.5": 12050, "120.50": 12050, "0.07": 7, "-3.10": -310}
	for in, want := range cases {
		m, err := Parse(in, PEN)
		if err != nil || m.Minor != want {
			t.Errorf("Parse(%q) = %d, %v; want %d", in, m.Minor, err, want)
		}
	}
	for _, in := range []string{"", ".5", "1.234", "1,50", "abc", "1e3", "99999999999"} {
		if _, err := Parse(in, PEN); !errors.Is(err, ErrAmount) {
			t.Errorf("Parse(%q) should fail with ErrAmount, got %v", in, err)
		}
	}
}

func TestDecimal(t *testing.T) {
	cases := map[int64]string{12050: "120.50", 7: "0.07", 0: "0.00", -310: "-3.10"}
	for in, want := range cases {
		if got := New(in, PEN).Decimal(); got != want {
			t.Errorf("Decimal(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestAddRejectsMixedCurrencies(t *testing.T) {
	if _, err := New(100, PEN).Add(New(100, Currency("USD"))); !errors.Is(err, ErrCurrency) {
		t.Fatalf("expected ErrCurrency, got %v", err)
	}
	sum, err := New(150, PEN).Add(New(250, PEN))
	if err != nil || sum.Minor != 400 {
		t.Fatalf("sum = %v, %v", sum, err)
	}
}

func TestParseCurrency(t *testing.T) {
	if c, err := ParseCurrency(" pen "); err != nil || c != PEN {
		t.Fatalf("got %q, %v", c, err)
	}
	if _, err := ParseCurrency("BRL"); !errors.Is(err, ErrCurrency) {
		t.Fatal("BRL is not sold in phase 1")
	}
}

func TestOnlySolesAreSupported(t *testing.T) {
	if c, err := ParseCurrency(" pen "); err != nil || c != PEN {
		t.Fatalf("PEN: %v %v", c, err)
	}
	if _, err := ParseCurrency("USD"); !errors.Is(err, ErrCurrency) {
		t.Fatalf("USD must be rejected, got %v", err)
	}
}
