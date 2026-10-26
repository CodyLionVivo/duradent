// Package application implements price lookups for the store and price list
// management for the admin panel.
package application

import (
	"context"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/pricing/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/pricing/ports"
)

const maxLookup = 200

type Service struct {
	lists  ports.PriceListRepository
	prices ports.PriceRepository
}

func NewService(lists ports.PriceListRepository, prices ports.PriceRepository) *Service {
	return &Service{lists: lists, prices: prices}
}

// Quote is the price shown for a variant: the unit price for the requested
// quantity plus the volume tiers in that currency.
type Quote struct {
	VariantID id.ID
	Unit      money.Money
	Tiers     []domain.Tier
	ListCode  string // list the price came from (may be the default list)
}

type QuoteQuery struct {
	ListCode   string // empty: the default list
	Currency   money.Currency
	VariantIDs []id.ID
	Qty        int
}

// Quote prices variants from the requested list, falling back to the default
// list for variants the requested list does not price. Variants priced in
// neither are left out: the store shows them as "price on request".
func (s *Service) Quote(ctx context.Context, q QuoteQuery) (map[id.ID]Quote, error) {
	if len(q.VariantIDs) > maxLookup {
		return nil, apperr.Invalid("pricing.too_many_variants", "variant_id", "at most 200 variants per request")
	}
	if q.Qty < 1 {
		q.Qty = 1
	}
	def, err := s.lists.Default(ctx)
	if err != nil {
		return nil, err
	}
	list := def
	if q.ListCode != "" && q.ListCode != def.Code {
		if list, err = s.lists.ByCode(ctx, q.ListCode); err != nil {
			return nil, err
		}
	}

	out := make(map[id.ID]Quote, len(q.VariantIDs))
	missing := q.VariantIDs
	for _, l := range []*domain.PriceList{list, def} {
		if len(missing) == 0 {
			break
		}
		found, err := s.prices.ForVariants(ctx, l.ID, missing)
		if err != nil {
			return nil, err
		}
		var still []id.ID
		for _, vid := range missing {
			vp, ok := found[vid]
			unit, priced := vp.UnitPrice(q.Currency, q.Qty)
			if !ok || !priced {
				still = append(still, vid)
				continue
			}
			out[vid] = Quote{VariantID: vid, Unit: unit, Tiers: vp.TiersIn(q.Currency), ListCode: l.Code}
		}
		missing = still
		if l == def {
			break
		}
	}
	return out, nil
}

// --- Admin -----------------------------------------------------------------------

func (s *Service) PriceLists(ctx context.Context) ([]*domain.PriceList, error) {
	return s.lists.List(ctx)
}

func (s *Service) PriceList(ctx context.Context, listID id.ID) (*domain.PriceList, error) {
	return s.lists.ByID(ctx, listID)
}

func (s *Service) CreatePriceList(ctx context.Context, code, name string, isDefault bool) (*domain.PriceList, error) {
	if _, err := s.lists.ByCode(ctx, code); err == nil {
		return nil, apperr.Conflict("pricing.code_taken", "code", "a list with this code already exists")
	} else if !apperr.IsKind(err, apperr.KindNotFound) {
		return nil, err
	}
	pl, err := domain.NewPriceList(code, name, isDefault)
	if err != nil {
		return nil, err
	}
	if err := s.lists.Save(ctx, pl); err != nil {
		return nil, err
	}
	return pl, nil
}

func (s *Service) UpdatePriceList(ctx context.Context, listID id.ID, code, name string, isDefault bool) (*domain.PriceList, error) {
	pl, err := s.lists.ByID(ctx, listID)
	if err != nil {
		return nil, err
	}
	if pl.IsDefault && !isDefault {
		return nil, apperr.Conflict("pricing.default_required", "is_default", "mark another list as default instead")
	}
	if code != pl.Code {
		if _, err := s.lists.ByCode(ctx, code); err == nil {
			return nil, apperr.Conflict("pricing.code_taken", "code", "a list with this code already exists")
		}
	}
	if err := pl.Update(code, name, isDefault); err != nil {
		return nil, err
	}
	if err := s.lists.Save(ctx, pl); err != nil {
		return nil, err
	}
	return pl, nil
}

// SetVariantPrices replaces every tier of one variant in one list. An empty
// slice removes the variant from the list.
func (s *Service) SetVariantPrices(ctx context.Context, listID, variantID id.ID, tiers []domain.Tier) (domain.VariantPrices, error) {
	if _, err := s.lists.ByID(ctx, listID); err != nil {
		return domain.VariantPrices{}, err
	}
	vp, err := domain.NewVariantPrices(listID, variantID, tiers)
	if err != nil {
		return domain.VariantPrices{}, err
	}
	if err := s.prices.Replace(ctx, vp); err != nil {
		return domain.VariantPrices{}, err
	}
	return vp, nil
}

// PricesInList returns the raw tiers (all currencies) of variants in a list.
func (s *Service) PricesInList(ctx context.Context, listID id.ID, variantIDs []id.ID) (map[id.ID]domain.VariantPrices, error) {
	if len(variantIDs) > maxLookup {
		return nil, apperr.Invalid("pricing.too_many_variants", "variant_id", "at most 200 variants per request")
	}
	if _, err := s.lists.ByID(ctx, listID); err != nil {
		return nil, err
	}
	return s.prices.ForVariants(ctx, listID, variantIDs)
}
