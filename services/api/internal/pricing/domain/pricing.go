// Package domain models price lists. Duradent sells B2B: each customer is
// assigned a list (public, clínica, distribuidor…) and every list prices each
// variant in soles, optionally with volume tiers.
package domain

import (
	"regexp"
	"slices"
	"strings"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

var listCodeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// PriceList is a named set of prices. Exactly one list is the default: it is
// shown to anonymous visitors and fills gaps in the other lists.
type PriceList struct {
	ID        id.ID
	Code      string
	Name      string
	IsDefault bool
}

func NewPriceList(code, name string, isDefault bool) (*PriceList, error) {
	pl := &PriceList{ID: id.New()}
	if err := pl.Update(code, name, isDefault); err != nil {
		return nil, err
	}
	return pl, nil
}

func (pl *PriceList) Update(code, name string, isDefault bool) error {
	code, name = strings.TrimSpace(code), strings.TrimSpace(name)
	if !listCodeRe.MatchString(code) {
		return apperr.Invalid("pricing.code_invalid", "code", "use lowercase letters, digits, '-' or '_'")
	}
	if name == "" || len(name) > 80 {
		return apperr.Invalid("pricing.name_invalid", "name", "name is required (max 80 characters)")
	}
	pl.Code, pl.Name, pl.IsDefault = code, name, isDefault
	return nil
}

// Tier is the unit price from MinQty units onwards.
type Tier struct {
	MinQty int
	Unit   money.Money
}

// VariantPrices holds every tier of one variant in one list, all currencies.
type VariantPrices struct {
	PriceListID id.ID
	VariantID   id.ID
	Tiers       []Tier
}

// NewVariantPrices validates a full replacement of a variant's tiers. Each
// currency that is priced must start at quantity 1, and higher tiers must not
// cost more per unit than lower ones.
func NewVariantPrices(listID, variantID id.ID, tiers []Tier) (VariantPrices, error) {
	sorted := slices.Clone(tiers)
	slices.SortFunc(sorted, func(a, b Tier) int {
		if c := strings.Compare(string(a.Unit.Currency), string(b.Unit.Currency)); c != 0 {
			return c
		}
		return a.MinQty - b.MinQty
	})
	for i, t := range sorted {
		if _, err := money.ParseCurrency(string(t.Unit.Currency)); err != nil {
			return VariantPrices{}, apperr.Invalid("pricing.currency_invalid", "tiers", "unsupported currency "+string(t.Unit.Currency))
		}
		if t.MinQty < 1 || t.MinQty > 100_000 {
			return VariantPrices{}, apperr.Invalid("pricing.min_qty_invalid", "tiers", "minimum quantity must be between 1 and 100000")
		}
		if !t.Unit.IsPositive() {
			return VariantPrices{}, apperr.Invalid("pricing.amount_invalid", "tiers", "price must be greater than zero")
		}
		first := i == 0 || sorted[i-1].Unit.Currency != t.Unit.Currency
		if first {
			if t.MinQty != 1 {
				return VariantPrices{}, apperr.Invalid("pricing.base_tier_missing", "tiers", "each currency needs a price for quantity 1")
			}
			continue
		}
		prev := sorted[i-1]
		if prev.MinQty == t.MinQty {
			return VariantPrices{}, apperr.Invalid("pricing.tier_duplicate", "tiers", "two tiers with the same minimum quantity")
		}
		if t.Unit.Minor > prev.Unit.Minor {
			return VariantPrices{}, apperr.Invalid("pricing.tier_not_decreasing", "tiers", "a volume tier cannot cost more than a smaller one")
		}
	}
	return VariantPrices{PriceListID: listID, VariantID: variantID, Tiers: sorted}, nil
}

// UnitPrice returns the unit price for qty units in currency c.
func (vp VariantPrices) UnitPrice(c money.Currency, qty int) (money.Money, bool) {
	var best *Tier
	for i := range vp.Tiers {
		t := &vp.Tiers[i]
		if t.Unit.Currency == c && t.MinQty <= qty && (best == nil || t.MinQty > best.MinQty) {
			best = t
		}
	}
	if best == nil {
		return money.Money{}, false
	}
	return best.Unit, true
}

// TiersIn returns the tiers of currency c in ascending quantity order.
func (vp VariantPrices) TiersIn(c money.Currency) []Tier {
	var out []Tier
	for _, t := range vp.Tiers {
		if t.Unit.Currency == c {
			out = append(out, t)
		}
	}
	return out
}
