package mysql_test

import (
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	catalogmysql "github.com/CodyLionVivo/duradent/services/api/internal/catalog/adapters/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysqltest"
	"github.com/CodyLionVivo/duradent/services/api/migrations"
)

var now = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

type repos struct {
	products   *catalogmysql.Products
	categories *catalogmysql.Categories
	attributes *catalogmysql.Attributes
}

func setup(t *testing.T) (*sql.DB, repos) {
	t.Helper()
	db := mysqltest.Start(t)
	if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	r := repos{catalogmysql.NewProducts(db), catalogmysql.NewCategories(db), catalogmysql.NewAttributes(db)}

	ctx := context.Background()
	tooth, _ := domain.NewAttribute("tooth", locale.Text{locale.ES: "Pieza", locale.EN: "Tooth"}, 0)
	for _, v := range []string{"11", "12", "13"} {
		if _, err := tooth.AddValue(v, locale.Text{locale.ES: v}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.attributes.Create(ctx, tooth); err != nil {
		t.Fatal(err)
	}
	return db, r
}

func es(name string) map[locale.Locale]domain.Translation {
	return map[locale.Locale]domain.Translation{locale.ES: {Name: name}}
}

func TestProductRoundTrip(t *testing.T) {
	_, r := setup(t)
	ctx := context.Background()

	cat, _ := domain.NewCategory(domain.CategoryDetails{Slug: "brackets", Texts: es("Brackets")}, now)
	if err := r.categories.Create(ctx, cat); err != nil {
		t.Fatal(err)
	}

	p, _ := domain.NewProduct(domain.Details{
		Slug: "bracket-roth-022", Brand: "Duradent", CategoryID: cat.ID,
		Texts: map[locale.Locale]domain.Translation{
			locale.ES: {Name: "Bracket Roth .022", Description: "Acero inoxidable"},
			locale.EN: {Name: "Roth bracket .022"},
		},
	}, now)
	v11, _ := p.AddVariant(domain.VariantInput{SKU: "BR-022-11", WeightG: 2, Options: map[string]string{"tooth": "11"}}, now)
	_, _ = p.AddVariant(domain.VariantInput{SKU: "BR-022-12", Options: map[string]string{"tooth": "12"}}, now)
	_, _ = p.AddMedia("products/x/a.webp", locale.Text{locale.ES: "Frente"}, now)
	if err := r.products.Create(ctx, p); err != nil {
		t.Fatal(err)
	}

	got, err := r.products.BySlug(ctx, "bracket-roth-022")
	if err != nil {
		t.Fatal(err)
	}
	if got.CategoryID != cat.ID || got.Texts[locale.EN].Name != "Roth bracket .022" || got.Texts[locale.ES].Description != "Acero inoxidable" {
		t.Fatalf("product fields: %+v", got)
	}
	if len(got.Variants) != 2 || got.Variants[0].ID != v11.ID || got.Variants[0].Options["tooth"] != "11" || !got.Variants[0].IsDefault {
		t.Fatalf("variants: %+v", got.Variants)
	}
	if len(got.Media) != 1 || got.Media[0].Alt[locale.ES] != "Frente" {
		t.Fatalf("media: %+v", got.Media)
	}

	// Archive a variant, change a SKU, publish: everything persists.
	if err := got.RemoveVariant(v11.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := got.UpdateVariant(got.Variants[1].ID, domain.VariantInput{SKU: "BR-022-12B", Options: map[string]string{"tooth": "13"}}, now); err != nil {
		t.Fatal(err)
	}
	if err := got.ChangeStatus(domain.StatusActive, now); err != nil {
		t.Fatal(err)
	}
	if err := r.products.Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, _ := r.products.ByID(ctx, p.ID)
	if again.Status != domain.StatusActive || !again.Variants[0].Archived || again.Variants[1].SKU != "BR-022-12B" ||
		again.Variants[1].Options["tooth"] != "13" || !again.Variants[1].IsDefault {
		t.Fatalf("after update: %+v", again.Variants)
	}
}

func TestSKUCollisionAcrossProductsIsAConflictAndOverwritesNothing(t *testing.T) {
	_, r := setup(t)
	ctx := context.Background()

	a, _ := domain.NewProduct(domain.Details{Slug: "producto-a", Texts: es("A")}, now)
	_, _ = a.AddVariant(domain.VariantInput{SKU: "SHARED-1", WeightG: 5}, now)
	if err := r.products.Create(ctx, a); err != nil {
		t.Fatal(err)
	}

	b, _ := domain.NewProduct(domain.Details{Slug: "producto-b", Texts: es("B")}, now)
	_, _ = b.AddVariant(domain.VariantInput{SKU: "SHARED-1", WeightG: 99}, now)
	err := r.products.Create(ctx, b)
	if e, ok := apperr.As(err); !ok || e.Code != "catalog.sku_taken" {
		t.Fatalf("got %v", err)
	}
	if _, err := r.products.BySlug(ctx, "producto-b"); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatal("failed create must roll back")
	}
	still, _ := r.products.BySlug(ctx, "producto-a")
	if still.Variants[0].WeightG != 5 {
		t.Fatalf("product A was modified: %+v", still.Variants[0])
	}

	dup, _ := domain.NewProduct(domain.Details{Slug: "producto-a", Texts: es("A2")}, now)
	if err := r.products.Create(ctx, dup); !apperr.IsKind(err, apperr.KindConflict) {
		t.Fatalf("duplicate slug: %v", err)
	}
}

func TestListFiltersSearchesAndPaginates(t *testing.T) {
	_, r := setup(t)
	ctx := context.Background()
	cat, _ := domain.NewCategory(domain.CategoryDetails{Slug: "alambres", Texts: es("Alambres")}, now)
	_ = r.categories.Create(ctx, cat)

	mk := func(slug, name, sku string, status domain.Status, category bool) {
		d := domain.Details{Slug: slug, Texts: es(name)}
		if category {
			d.CategoryID = cat.ID
		}
		p, _ := domain.NewProduct(d, now)
		_, _ = p.AddVariant(domain.VariantInput{SKU: sku}, now)
		_ = p.ChangeStatus(status, now)
		if err := r.products.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	mk("arco-niti-014", "Arco NiTi 0.014", "AR-NITI-014", domain.StatusActive, true)
	mk("arco-niti-016", "Arco NiTi 0.016", "AR-NITI-016", domain.StatusActive, true)
	mk("arco-acero-016", "Arco acero 0.016", "AR-SS-016", domain.StatusDraft, true)
	mk("ligaduras-elasticas", "Ligaduras elásticas", "LIG-100_X", domain.StatusActive, false)

	active := []domain.Status{domain.StatusActive}
	list, total, err := r.products.List(ctx, ports.ProductFilter{Statuses: active, Limit: 2})
	if err != nil || total != 3 || len(list) != 2 || list[0].Slug != "arco-niti-014" {
		t.Fatalf("page 1: %d %v %v", total, slugs(list), err)
	}
	list, _, _ = r.products.List(ctx, ports.ProductFilter{Statuses: active, Limit: 2, Offset: 2})
	if len(list) != 1 || list[0].Slug != "ligaduras-elasticas" {
		t.Fatalf("page 2: %v", slugs(list))
	}

	list, total, _ = r.products.List(ctx, ports.ProductFilter{Statuses: active, Query: "niti", Locale: locale.EN, Limit: 10})
	if total != 2 {
		t.Fatalf("search by name falls back to Spanish: %v", slugs(list))
	}
	_, total, _ = r.products.List(ctx, ports.ProductFilter{Query: "ss-016", Limit: 10})
	if total != 1 {
		t.Fatalf("search by SKU, any status: %d", total)
	}
	_, total, _ = r.products.List(ctx, ports.ProductFilter{Query: "100_", Limit: 10})
	if total != 1 {
		t.Fatalf("LIKE wildcards must be escaped: %d", total)
	}
	_, total, _ = r.products.List(ctx, ports.ProductFilter{Statuses: active, CategoryIDs: []id.ID{cat.ID}, Limit: 10})
	if total != 2 {
		t.Fatalf("active products in alambres: %d", total)
	}
}

func slugs(ps []*domain.Product) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Slug
	}
	return out
}

func TestCategoryAndAttributePersistence(t *testing.T) {
	_, r := setup(t)
	ctx := context.Background()

	root, _ := domain.NewCategory(domain.CategoryDetails{Slug: "brackets", Texts: es("Brackets")}, now)
	child, _ := domain.NewCategory(domain.CategoryDetails{Slug: "ceramicos", ParentID: root.ID, SortOrder: 2, Texts: es("Cerámicos")}, now)
	for _, c := range []*domain.Category{root, child} {
		if err := r.categories.Create(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.categories.BySlug(ctx, "ceramicos")
	if err != nil || got.ParentID != root.ID || got.Name(locale.EN) != "Cerámicos" {
		t.Fatalf("category: %+v %v", got, err)
	}

	tooth, _ := r.attributes.ByCode(ctx, "tooth")
	if len(tooth.Values) != 3 || tooth.Names[locale.EN] != "Tooth" {
		t.Fatalf("attribute: %+v", tooth)
	}
	if _, err := tooth.AddValue("14", locale.Text{locale.ES: "14"}); err != nil {
		t.Fatal(err)
	}
	if err := r.attributes.Update(ctx, tooth); err != nil {
		t.Fatal(err)
	}
	tooth, _ = r.attributes.ByCode(ctx, "tooth")
	if len(tooth.Values) != 4 {
		t.Fatalf("new value not saved: %+v", tooth.Values)
	}
}

func TestFiltersAndFacets(t *testing.T) {
	_, r := setup(t)
	ctx := context.Background()
	arch, _ := domain.NewAttribute("arch", locale.Text{locale.ES: "Arcada"}, 1)
	for _, v := range []string{"upper", "lower"} {
		if _, err := arch.AddValue(v, locale.Text{locale.ES: v}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.attributes.Create(ctx, arch); err != nil {
		t.Fatal(err)
	}
	cat, _ := domain.NewCategory(domain.CategoryDetails{Slug: "brackets", Texts: es("Brackets")}, now)
	_ = r.categories.Create(ctx, cat)

	mk := func(slug, brand string, status domain.Status, category bool, variants ...map[string]string) {
		d := domain.Details{Slug: slug, Brand: brand, Texts: es(slug)}
		if category {
			d.CategoryID = cat.ID
		}
		p, _ := domain.NewProduct(d, now)
		for i, opts := range variants {
			if _, err := p.AddVariant(domain.VariantInput{SKU: slug + "-" + string(rune('a'+i)), Options: opts}, now); err != nil {
				t.Fatal(err)
			}
		}
		_ = p.ChangeStatus(status, now)
		if err := r.products.Create(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	mk("p1", "Acme", domain.StatusActive, true, map[string]string{"tooth": "11", "arch": "upper"}, map[string]string{"tooth": "12", "arch": "lower"})
	mk("p2", "Acme", domain.StatusActive, false, map[string]string{"tooth": "11", "arch": "lower"})
	mk("p3", "Beta", domain.StatusActive, true, map[string]string{"tooth": "13", "arch": "upper"})
	mk("p4", "Beta", domain.StatusDraft, true, map[string]string{"tooth": "11", "arch": "upper"})

	active := []domain.Status{domain.StatusActive}
	list := func(f ports.ProductFilter) []string {
		t.Helper()
		f.Statuses, f.Limit = active, 10
		ps, total, err := r.products.List(ctx, f)
		if err != nil || total != len(ps) {
			t.Fatalf("list %+v: %d %v", f, total, err)
		}
		return slugs(ps)
	}
	if got := list(ports.ProductFilter{Brands: []string{"Acme"}}); !slices.Equal(got, []string{"p1", "p2"}) {
		t.Errorf("brand: %v", got)
	}
	// Different attributes must hold for the same variant.
	if got := list(ports.ProductFilter{Attributes: map[string][]string{"tooth": {"11"}, "arch": {"upper"}}}); !slices.Equal(got, []string{"p1"}) {
		t.Errorf("same variant: %v", got)
	}
	// Values of one attribute are alternatives.
	if got := list(ports.ProductFilter{Attributes: map[string][]string{"tooth": {"11", "13"}}}); !slices.Equal(got, []string{"p1", "p2", "p3"}) {
		t.Errorf("any value: %v", got)
	}
	if got := list(ports.ProductFilter{Sort: ports.SortNewest}); len(got) != 3 {
		t.Errorf("newest: %v", got)
	}

	f, err := r.products.Facets(ctx, ports.ProductFilter{
		Statuses: active, Brands: []string{"Acme"}, Attributes: map[string][]string{"tooth": {"11"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	counts := func(fc []ports.FacetCount) map[string]int {
		out := map[string]int{}
		for _, c := range fc {
			out[c.Value] = c.Count
		}
		return out
	}
	// Brands ignore the brand selection: Beta has no active tooth-11 product.
	if got := counts(f.Brands); len(got) != 1 || got["Acme"] != 2 {
		t.Errorf("brands: %v", got)
	}
	// Tooth ignores its own selection: Acme products have 11 (p1, p2) and 12 (p1).
	if got := counts(f.Attributes["tooth"]); got["11"] != 2 || got["12"] != 1 || got["13"] != 0 {
		t.Errorf("tooth: %v", got)
	}
	// Arch applies tooth 11 to the same variant: p1 upper, p2 lower.
	if got := counts(f.Attributes["arch"]); got["upper"] != 1 || got["lower"] != 1 {
		t.Errorf("arch: %v", got)
	}
	if f.Categories[cat.ID] != 1 {
		t.Errorf("categories: %v", f.Categories)
	}
}
