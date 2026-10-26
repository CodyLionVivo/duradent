// Package ports declares the persistence the pricing context depends on.
// Variant ids are opaque here: pricing never reads the catalog.
package ports

import (
	"context"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/pricing/domain"
)

type PriceListRepository interface {
	// Save inserts or updates a list. When pl.IsDefault is set, every other
	// list loses the flag in the same transaction.
	Save(ctx context.Context, pl *domain.PriceList) error
	ByID(ctx context.Context, listID id.ID) (*domain.PriceList, error)
	ByCode(ctx context.Context, code string) (*domain.PriceList, error)
	Default(ctx context.Context) (*domain.PriceList, error)
	List(ctx context.Context) ([]*domain.PriceList, error)
}

type PriceRepository interface {
	// Replace swaps every tier of the variant in the list atomically.
	Replace(ctx context.Context, vp domain.VariantPrices) error
	// ForVariants returns prices keyed by variant id; variants without
	// prices in the list are absent from the map.
	ForVariants(ctx context.Context, listID id.ID, variantIDs []id.ID) (map[id.ID]domain.VariantPrices, error)
}
