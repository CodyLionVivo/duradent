// Package application implements the catalog use cases for the storefront
// (read-only, active products) and the admin panel (full editing).
package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

const (
	defaultPageSize = 24
	maxPageSize     = 100
	uploadTTL       = 15 * time.Minute
)

// allowedImageTypes maps accepted upload content types to file extensions.
var allowedImageTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/avif": ".avif",
}

type Service struct {
	products   ports.ProductRepository
	categories ports.CategoryRepository
	attributes ports.AttributeRepository
	media      ports.MediaStorage
	now        func() time.Time
}

func NewService(p ports.ProductRepository, c ports.CategoryRepository, a ports.AttributeRepository, m ports.MediaStorage, now func() time.Time) *Service {
	return &Service{products: p, categories: c, attributes: a, media: m, now: now}
}

// MediaURL resolves a stored object key to the URL browsers load it from.
func (s *Service) MediaURL(key string) string { return s.media.PublicURL(key) }

// --- Storefront ---------------------------------------------------------------

type Page struct {
	Limit  int
	Offset int
}

func (p Page) normalized() Page {
	if p.Limit <= 0 {
		p.Limit = defaultPageSize
	}
	if p.Limit > maxPageSize {
		p.Limit = maxPageSize
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p
}

type BrowseQuery struct {
	CategorySlug string // includes its subcategories
	Query        string
	Locale       locale.Locale
	Page         Page
}

// BrowseProducts lists active products for the store.
func (s *Service) BrowseProducts(ctx context.Context, q BrowseQuery) ([]*domain.Product, int, error) {
	f := ports.ProductFilter{
		Statuses: []domain.Status{domain.StatusActive},
		Query:    strings.TrimSpace(q.Query),
		Locale:   q.Locale,
	}
	if q.CategorySlug != "" {
		ids, err := s.categorySubtree(ctx, q.CategorySlug)
		if err != nil {
			return nil, 0, err
		}
		f.CategoryIDs = ids
	}
	p := q.Page.normalized()
	f.Limit, f.Offset = p.Limit, p.Offset
	return s.products.List(ctx, f)
}

func (s *Service) categorySubtree(ctx context.Context, slug string) ([]id.ID, error) {
	c, err := s.categories.BySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	all, err := s.categories.List(ctx)
	if err != nil {
		return nil, err
	}
	return domain.Descendants(all, c.ID), nil
}

// ProductForStore returns an active product by slug; drafts and archived
// products are reported as not found.
func (s *Service) ProductForStore(ctx context.Context, slug string) (*domain.Product, error) {
	p, err := s.products.BySlug(ctx, slug)
	if err != nil {
		return nil, err
	}
	if p.Status != domain.StatusActive {
		return nil, apperr.NotFound("catalog.product_not_found", "product not found")
	}
	return p, nil
}

func (s *Service) Categories(ctx context.Context) ([]*domain.Category, error) {
	return s.categories.List(ctx)
}

func (s *Service) Attributes(ctx context.Context) ([]*domain.Attribute, error) {
	return s.attributes.List(ctx)
}

// VariantRef is a variant with the product it belongs to.
type VariantRef struct {
	Product *domain.Product
	Variant domain.Variant
}

// Variants finds variants by id (any status); unknown ids are absent.
func (s *Service) Variants(ctx context.Context, variantIDs []id.ID) (map[id.ID]VariantRef, error) {
	if len(variantIDs) > 500 {
		return nil, apperr.Invalid("catalog.too_many_variants", "variant_id", "at most 500 variants per request")
	}
	products, err := s.products.ByVariantIDs(ctx, variantIDs)
	if err != nil {
		return nil, err
	}
	want := make(map[id.ID]bool, len(variantIDs))
	for _, v := range variantIDs {
		want[v] = true
	}
	out := make(map[id.ID]VariantRef, len(variantIDs))
	for _, p := range products {
		for _, v := range p.Variants {
			if want[v.ID] {
				out[v.ID] = VariantRef{Product: p, Variant: v}
			}
		}
	}
	return out, nil
}

// --- Admin: products -----------------------------------------------------------

type AdminProductQuery struct {
	Statuses []domain.Status // empty: every status
	Query    string
	Page     Page
}

func (s *Service) AdminProducts(ctx context.Context, q AdminProductQuery) ([]*domain.Product, int, error) {
	p := q.Page.normalized()
	return s.products.List(ctx, ports.ProductFilter{
		Statuses: q.Statuses, Query: strings.TrimSpace(q.Query), Locale: locale.Default,
		Limit: p.Limit, Offset: p.Offset,
	})
}

func (s *Service) Product(ctx context.Context, productID id.ID) (*domain.Product, error) {
	return s.products.ByID(ctx, productID)
}

func (s *Service) CreateProduct(ctx context.Context, d domain.Details) (*domain.Product, error) {
	if err := s.checkCategory(ctx, d.CategoryID); err != nil {
		return nil, err
	}
	p, err := domain.NewProduct(d, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.products.Create(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) UpdateProduct(ctx context.Context, productID id.ID, d domain.Details) (*domain.Product, error) {
	if err := s.checkCategory(ctx, d.CategoryID); err != nil {
		return nil, err
	}
	return s.mutate(ctx, productID, func(p *domain.Product) error { return p.UpdateDetails(d, s.now()) })
}

func (s *Service) ChangeStatus(ctx context.Context, productID id.ID, to domain.Status) (*domain.Product, error) {
	return s.mutate(ctx, productID, func(p *domain.Product) error { return p.ChangeStatus(to, s.now()) })
}

func (s *Service) AddVariant(ctx context.Context, productID id.ID, in domain.VariantInput) (*domain.Product, error) {
	if err := s.checkOptions(ctx, in.Options); err != nil {
		return nil, err
	}
	return s.mutate(ctx, productID, func(p *domain.Product) error {
		_, err := p.AddVariant(in, s.now())
		return err
	})
}

func (s *Service) UpdateVariant(ctx context.Context, productID, variantID id.ID, in domain.VariantInput) (*domain.Product, error) {
	if err := s.checkOptions(ctx, in.Options); err != nil {
		return nil, err
	}
	return s.mutate(ctx, productID, func(p *domain.Product) error { return p.UpdateVariant(variantID, in, s.now()) })
}

func (s *Service) RemoveVariant(ctx context.Context, productID, variantID id.ID) (*domain.Product, error) {
	return s.mutate(ctx, productID, func(p *domain.Product) error { return p.RemoveVariant(variantID, s.now()) })
}

func (s *Service) SetDefaultVariant(ctx context.Context, productID, variantID id.ID) (*domain.Product, error) {
	return s.mutate(ctx, productID, func(p *domain.Product) error { return p.SetDefaultVariant(variantID, s.now()) })
}

// RequestImageUpload returns a presigned URL the admin browser PUTs the image
// to. The image is attached to the product afterwards with AttachImage.
func (s *Service) RequestImageUpload(ctx context.Context, productID id.ID, contentType string) (key string, up ports.Upload, err error) {
	ext, ok := allowedImageTypes[contentType]
	if !ok {
		return "", ports.Upload{}, apperr.Invalid("catalog.image_type", "content_type", "use JPEG, PNG, WebP or AVIF")
	}
	if _, err := s.products.ByID(ctx, productID); err != nil {
		return "", ports.Upload{}, err
	}
	key = fmt.Sprintf("products/%s/%s%s", productID, id.New(), ext)
	up, err = s.media.PresignUpload(ctx, key, contentType, uploadTTL)
	return key, up, err
}

func (s *Service) AttachImage(ctx context.Context, productID id.ID, key string, alt locale.Text) (*domain.Product, error) {
	if !strings.HasPrefix(key, fmt.Sprintf("products/%s/", productID)) {
		return nil, apperr.Invalid("catalog.media_key_foreign", "key", "image was uploaded for another product")
	}
	return s.mutate(ctx, productID, func(p *domain.Product) error {
		_, err := p.AddMedia(key, alt, s.now())
		return err
	})
}

func (s *Service) RemoveImage(ctx context.Context, productID, mediaID id.ID) (*domain.Product, error) {
	return s.mutate(ctx, productID, func(p *domain.Product) error { return p.RemoveMedia(mediaID, s.now()) })
}

func (s *Service) mutate(ctx context.Context, productID id.ID, fn func(*domain.Product) error) (*domain.Product, error) {
	p, err := s.products.ByID(ctx, productID)
	if err != nil {
		return nil, err
	}
	if err := fn(p); err != nil {
		return nil, err
	}
	if err := s.products.Update(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) checkCategory(ctx context.Context, categoryID id.ID) error {
	if categoryID.IsNil() {
		return nil
	}
	if _, err := s.categories.ByID(ctx, categoryID); err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return apperr.Invalid("catalog.category_unknown", "category_id", "category does not exist")
		}
		return err
	}
	return nil
}

func (s *Service) checkOptions(ctx context.Context, options map[string]string) error {
	if len(options) == 0 {
		return nil
	}
	attrs, err := s.attributes.List(ctx)
	if err != nil {
		return err
	}
	return domain.CheckOptions(attrs, options)
}

// --- Admin: categories and attributes ----------------------------------------

func (s *Service) CreateCategory(ctx context.Context, d domain.CategoryDetails) (*domain.Category, error) {
	if err := s.checkParent(ctx, id.Nil, d.ParentID); err != nil {
		return nil, err
	}
	c, err := domain.NewCategory(d, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.categories.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) UpdateCategory(ctx context.Context, categoryID id.ID, d domain.CategoryDetails) (*domain.Category, error) {
	c, err := s.categories.ByID(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	if err := s.checkParent(ctx, categoryID, d.ParentID); err != nil {
		return nil, err
	}
	if err := c.Update(d, s.now()); err != nil {
		return nil, err
	}
	if err := s.categories.Update(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) checkParent(ctx context.Context, child, parent id.ID) error {
	if parent.IsNil() {
		return nil
	}
	all, err := s.categories.List(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, c := range all {
		if c.ID == parent {
			found = true
			break
		}
	}
	if !found {
		return apperr.Invalid("catalog.category_unknown", "parent_id", "parent category does not exist")
	}
	if !child.IsNil() && domain.WouldCycle(all, child, parent) {
		return apperr.Invalid("catalog.category_cycle", "parent_id", "a category cannot be moved under its own subcategory")
	}
	return nil
}

func (s *Service) CreateAttribute(ctx context.Context, code string, names locale.Text, sortOrder int) (*domain.Attribute, error) {
	a, err := domain.NewAttribute(code, names, sortOrder)
	if err != nil {
		return nil, err
	}
	if err := s.attributes.Create(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Service) AddAttributeValue(ctx context.Context, code, valueCode string, labels locale.Text) (*domain.Attribute, error) {
	a, err := s.attributes.ByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if _, err := a.AddValue(valueCode, labels); err != nil {
		return nil, err
	}
	if err := s.attributes.Update(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}
