// Package ports declares what the catalog needs from the outside world.
package ports

import (
	"context"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

// ProductRepository persists whole product aggregates (variants and media
// included). Create and Update return apperr Conflict when the slug or a SKU
// is already used, and NotFound for unknown ids.
type ProductRepository interface {
	Create(ctx context.Context, p *domain.Product) error
	Update(ctx context.Context, p *domain.Product) error
	ByID(ctx context.Context, productID id.ID) (*domain.Product, error)
	BySlug(ctx context.Context, slug string) (*domain.Product, error)
	List(ctx context.Context, f ProductFilter) ([]*domain.Product, int, error)
}

type ProductFilter struct {
	Statuses    []domain.Status
	CategoryIDs []id.ID       // any of them; empty means all
	Query       string        // matched against names and SKUs
	Locale      locale.Locale // language used to match Query
	Limit       int
	Offset      int
}

type CategoryRepository interface {
	Create(ctx context.Context, c *domain.Category) error
	Update(ctx context.Context, c *domain.Category) error
	ByID(ctx context.Context, categoryID id.ID) (*domain.Category, error)
	BySlug(ctx context.Context, slug string) (*domain.Category, error)
	List(ctx context.Context) ([]*domain.Category, error)
}

type AttributeRepository interface {
	Create(ctx context.Context, a *domain.Attribute) error
	Update(ctx context.Context, a *domain.Attribute) error
	ByCode(ctx context.Context, code string) (*domain.Attribute, error)
	List(ctx context.Context) ([]*domain.Attribute, error)
}

// MediaStorage hands out direct-to-bucket upload URLs, so image bytes never
// pass through the API, and resolves object keys to public URLs (CloudFront).
type MediaStorage interface {
	PresignUpload(ctx context.Context, key, contentType string, ttl time.Duration) (Upload, error)
	PublicURL(key string) string
}

type Upload struct {
	URL     string
	Method  string
	Headers map[string]string
}
