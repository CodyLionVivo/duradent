package domain

import (
	"testing"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

func pen(minor int64) money.Money { return money.New(minor, money.PEN) }

func code(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

func TestVariantPricesValidation(t *testing.T) {
	l, v := id.New(), id.New()
	cases := map[string][]Tier{
		"pricing.base_tier_missing":   {{MinQty: 10, Unit: pen(1000)}},
		"pricing.amount_invalid":      {{MinQty: 1, Unit: pen(0)}},
		"pricing.tier_duplicate":      {{MinQty: 1, Unit: pen(1000)}, {MinQty: 1, Unit: pen(900)}},
		"pricing.tier_not_decreasing": {{MinQty: 1, Unit: pen(1000)}, {MinQty: 10, Unit: pen(1100)}},
		"pricing.min_qty_invalid":     {{MinQty: 0, Unit: pen(1000)}},
		"pricing.currency_invalid":    {{MinQty: 1, Unit: money.New(1000, "USD")}},
	}
	for want, tiers := range cases {
		if _, err := NewVariantPrices(l, v, tiers); code(err) != want {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestUnitPricePicksHighestApplicableTier(t *testing.T) {
	vp, err := NewVariantPrices(id.New(), id.New(), []Tier{
		{MinQty: 50, Unit: pen(800)},
		{MinQty: 1, Unit: pen(1000)},
		{MinQty: 10, Unit: pen(900)},
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[int]int64{1: 1000, 9: 1000, 10: 900, 49: 900, 50: 800, 500: 800}
	for qty, want := range cases {
		got, ok := vp.UnitPrice(money.PEN, qty)
		if !ok || got.Minor != want {
			t.Errorf("qty %d: got %v, want %d", qty, got, want)
		}
	}
	if len(vp.TiersIn(money.PEN)) != 3 || vp.TiersIn(money.PEN)[0].MinQty != 1 {
		t.Errorf("tiers should be sorted ascending: %+v", vp.TiersIn(money.PEN))
	}
}

func TestPriceListCode(t *testing.T) {
	if _, err := NewPriceList("Clínicas", "Clínicas", false); code(err) != "pricing.code_invalid" {
		t.Fatalf("got %v", err)
	}
	if _, err := NewPriceList("clinicas", "Clínicas", false); err != nil {
		t.Fatal(err)
	}
}
