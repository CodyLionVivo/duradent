package application_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

// --- in-memory fakes of the ports ---------------------------------------------

type memProducts struct {
	byID       map[id.ID]domain.Product
	lastFacets ports.ProductFilter
}

// Facets records the filter it was asked for (the counting itself is tested
// against MySQL in the adapter).
func (m *memProducts) Facets(_ context.Context, f ports.ProductFilter) (ports.Facets, error) {
	m.lastFacets = f
	return ports.Facets{}, nil
}

func (m *memProducts) Create(_ context.Context, p *domain.Product) error {
	for _, o := range m.byID {
		if o.Slug == p.Slug {
			return apperr.Conflict("catalog.slug_taken", "slug", "taken")
		}
	}
	m.byID[p.ID] = *p
	return nil
}

func (m *memProducts) Update(_ context.Context, p *domain.Product) error {
	m.byID[p.ID] = *p
	return nil
}

func (m *memProducts) ByID(_ context.Context, pid id.ID) (*domain.Product, error) {
	p, ok := m.byID[pid]
	if !ok {
		return nil, apperr.NotFound("catalog.product_not_found", "nope")
	}
	p.Variants = slices.Clone(p.Variants)
	return &p, nil
}

func (m *memProducts) BySlug(ctx context.Context, slug string) (*domain.Product, error) {
	for _, p := range m.byID {
		if p.Slug == slug {
			return m.ByID(ctx, p.ID)
		}
	}
	return nil, apperr.NotFound("catalog.product_not_found", "nope")
}

func (m *memProducts) List(_ context.Context, f ports.ProductFilter) ([]*domain.Product, int, error) {
	var out []*domain.Product
	for _, p := range m.byID {
		if len(f.Statuses) > 0 && !slices.Contains(f.Statuses, p.Status) {
			continue
		}
		if len(f.CategoryIDs) > 0 && !slices.Contains(f.CategoryIDs, p.CategoryID) {
			continue
		}
		if f.Query != "" && !strings.Contains(strings.ToLower(p.Name(f.Locale)), strings.ToLower(f.Query)) {
			continue
		}
		out = append(out, &p)
	}
	return out, len(out), nil
}

func (m *memProducts) ByVariantIDs(_ context.Context, ids []id.ID) ([]*domain.Product, error) {
	var out []*domain.Product
	for _, p := range m.byID {
		for _, v := range p.Variants {
			if slices.Contains(ids, v.ID) {
				cp := p
				out = append(out, &cp)
				break
			}
		}
	}
	return out, nil
}

type memCategories struct{ all []*domain.Category }

func (m *memCategories) Create(_ context.Context, c *domain.Category) error {
	m.all = append(m.all, c)
	return nil
}
func (m *memCategories) Update(context.Context, *domain.Category) error { return nil }
func (m *memCategories) ByID(_ context.Context, cid id.ID) (*domain.Category, error) {
	for _, c := range m.all {
		if c.ID == cid {
			return c, nil
		}
	}
	return nil, apperr.NotFound("catalog.category_not_found", "nope")
}
func (m *memCategories) BySlug(_ context.Context, slug string) (*domain.Category, error) {
	for _, c := range m.all {
		if c.Slug == slug {
			return c, nil
		}
	}
	return nil, apperr.NotFound("catalog.category_not_found", "nope")
}
func (m *memCategories) List(context.Context) ([]*domain.Category, error) { return m.all, nil }

type memAttributes struct{ all []*domain.Attribute }

func (m *memAttributes) Create(_ context.Context, a *domain.Attribute) error {
	m.all = append(m.all, a)
	return nil
}
func (m *memAttributes) Update(context.Context, *domain.Attribute) error { return nil }
func (m *memAttributes) ByCode(_ context.Context, code string) (*domain.Attribute, error) {
	for _, a := range m.all {
		if a.Code == code {
			return a, nil
		}
	}
	return nil, apperr.NotFound("catalog.attribute_not_found", "nope")
}
func (m *memAttributes) List(context.Context) ([]*domain.Attribute, error) { return m.all, nil }

type fakeMedia struct{ presigned []string }

func (f *fakeMedia) PresignUpload(_ context.Context, key, _ string, _ time.Duration) (ports.Upload, error) {
	f.presigned = append(f.presigned, key)
	return ports.Upload{URL: "https://bucket.test/" + key, Method: "PUT"}, nil
}
func (f *fakeMedia) PublicURL(key string) string { return "https://cdn.test/" + key }

// --- helpers --------------------------------------------------------------------

type fixture struct {
	svc        *application.Service
	products   *memProducts
	categories *memCategories
	media      *fakeMedia
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{
		products:   &memProducts{byID: map[id.ID]domain.Product{}},
		categories: &memCategories{},
		media:      &fakeMedia{},
	}
	attrs := &memAttributes{}
	now := func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }
	f.svc = application.NewService(f.products, f.categories, attrs, f.media, now)

	ctx := context.Background()
	if _, err := f.svc.CreateAttribute(ctx, "tooth", locale.Text{locale.ES: "Pieza"}, 0); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"11", "12"} {
		if _, err := f.svc.AddAttributeValue(ctx, "tooth", v, locale.Text{locale.ES: v}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func es(name string) map[locale.Locale]domain.Translation {
	return map[locale.Locale]domain.Translation{locale.ES: {Name: name}}
}

func (f fixture) activeProduct(t *testing.T, slug, name string, categoryID id.ID) *domain.Product {
	t.Helper()
	ctx := context.Background()
	p, err := f.svc.CreateProduct(ctx, domain.Details{Slug: slug, Texts: es(name), CategoryID: categoryID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AddVariant(ctx, p.ID, domain.VariantInput{SKU: strings.ToUpper(slug) + "-11", Options: map[string]string{"tooth": "11"}}); err != nil {
		t.Fatal(err)
	}
	p, err = f.svc.ChangeStatus(ctx, p.ID, domain.StatusActive)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// --- tests ----------------------------------------------------------------------

func TestStoreOnlySeesActiveProducts(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	active := f.activeProduct(t, "bracket-roth", "Bracket Roth", id.Nil)
	draft, err := f.svc.CreateProduct(ctx, domain.Details{Slug: "bracket-mbt", Texts: es("Bracket MBT")})
	if err != nil {
		t.Fatal(err)
	}

	res, err := f.svc.BrowseProducts(ctx, application.BrowseQuery{Locale: locale.ES})
	if err != nil || res.Total != 1 || res.Products[0].ID != active.ID {
		t.Fatalf("browse = %+v, %v", res, err)
	}
	if _, err := f.svc.ProductForStore(ctx, draft.Slug); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("draft must be hidden from the store, got %v", err)
	}
	if _, err := f.svc.ProductForStore(ctx, active.Slug); err != nil {
		t.Fatal(err)
	}
}

func TestBrowseByCategoryIncludesSubcategories(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	brackets, _ := f.svc.CreateCategory(ctx, domain.CategoryDetails{Slug: "brackets", Texts: es("Brackets")})
	metal, _ := f.svc.CreateCategory(ctx, domain.CategoryDetails{Slug: "metalicos", ParentID: brackets.ID, Texts: es("Metálicos")})
	wires, _ := f.svc.CreateCategory(ctx, domain.CategoryDetails{Slug: "alambres", Texts: es("Alambres")})

	f.activeProduct(t, "bracket-roth", "Bracket Roth", metal.ID)
	f.activeProduct(t, "arco-niti", "Arco NiTi", wires.ID)

	res, err := f.svc.BrowseProducts(ctx, application.BrowseQuery{CategorySlug: "brackets"})
	if err != nil || res.Total != 1 {
		t.Fatalf("brackets subtree should contain 1 product, got %d (%v)", res.Total, err)
	}
	if _, err := f.svc.BrowseProducts(ctx, application.BrowseQuery{CategorySlug: "nope"}); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("unknown category: %v", err)
	}
}

func TestVariantOptionsAreCheckedAgainstAttributes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p, _ := f.svc.CreateProduct(ctx, domain.Details{Slug: "bracket-roth", Texts: es("Bracket Roth")})

	_, err := f.svc.AddVariant(ctx, p.ID, domain.VariantInput{SKU: "X-1", Options: map[string]string{"tooth": "48"}})
	if e, ok := apperr.As(err); !ok || e.Code != "catalog.attribute_value_unknown" {
		t.Fatalf("got %v", err)
	}
}

func TestCreateProductRejectsUnknownCategory(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.CreateProduct(context.Background(), domain.Details{Slug: "x", Texts: es("X"), CategoryID: id.New()})
	if e, ok := apperr.As(err); !ok || e.Code != "catalog.category_unknown" {
		t.Fatalf("got %v", err)
	}
}

func TestCategoryCannotMoveUnderItsChild(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	root, _ := f.svc.CreateCategory(ctx, domain.CategoryDetails{Slug: "brackets", Texts: es("Brackets")})
	child, _ := f.svc.CreateCategory(ctx, domain.CategoryDetails{Slug: "metalicos", ParentID: root.ID, Texts: es("Metálicos")})

	_, err := f.svc.UpdateCategory(ctx, root.ID, domain.CategoryDetails{Slug: "brackets", ParentID: child.ID, Texts: es("Brackets")})
	if e, ok := apperr.As(err); !ok || e.Code != "catalog.category_cycle" {
		t.Fatalf("got %v", err)
	}
}

func TestImageUploadFlow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.activeProduct(t, "bracket-roth", "Bracket Roth", id.Nil)

	if _, _, err := f.svc.RequestImageUpload(ctx, p.ID, "image/gif"); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("gif should be rejected: %v", err)
	}
	key, up, err := f.svc.RequestImageUpload(ctx, p.ID, "image/webp")
	if err != nil || up.Method != http.MethodPut || !strings.HasSuffix(key, ".webp") {
		t.Fatalf("presign: %q %+v %v", key, up, err)
	}

	other := f.activeProduct(t, "arco-niti", "Arco NiTi", id.Nil)
	if _, err := f.svc.AttachImage(ctx, other.ID, key, nil); !apperr.IsKind(err, apperr.KindInvalid) {
		t.Fatalf("attaching another product's key must fail: %v", err)
	}

	updated, err := f.svc.AttachImage(ctx, p.ID, key, locale.Text{locale.ES: "Vista frontal"})
	if err != nil || len(updated.Media) != 1 {
		t.Fatalf("attach: %+v %v", updated, err)
	}
	if got := f.svc.MediaURL(key); got != "https://cdn.test/"+key {
		t.Fatalf("media url = %q", got)
	}
}

func TestBrowseCleansFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.activeProduct(t, "bracket-roth", "Bracket Roth", id.Nil)
	_, err := f.svc.BrowseProducts(ctx, application.BrowseQuery{
		Brands: []string{" Duradent ", "Duradent", "", strings.Repeat("x", 81)},
		Attributes: map[string][]string{
			"slot":         {"022", "022", "018"},
			"Bad Code!":    {"x"},
			"arch":         {""},
			"prescription": {"roth"},
		},
		Sort: "price; DROP TABLE products",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := f.products.lastFacets
	if !slices.Equal(got.Brands, []string{"Duradent"}) {
		t.Errorf("brands: %q", got.Brands)
	}
	if len(got.Attributes) != 2 || !slices.Equal(got.Attributes["slot"], []string{"022", "018"}) || got.Attributes["prescription"][0] != "roth" {
		t.Errorf("attributes: %v", got.Attributes)
	}
	if got.Sort != ports.SortName {
		t.Errorf("unknown sort falls back to name: %q", got.Sort)
	}
	if _, err := f.svc.BrowseProducts(ctx, application.BrowseQuery{Sort: ports.SortNewest}); err != nil || f.products.lastFacets.Sort != ports.SortNewest {
		t.Errorf("newest: %v %q", err, f.products.lastFacets.Sort)
	}
}
