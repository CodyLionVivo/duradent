package domain

import (
	"testing"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

var now = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func details() Details {
	return Details{
		Slug:  "bracket-metalico-roth-022",
		Brand: "Duradent",
		Texts: map[locale.Locale]Translation{
			locale.ES: {Name: "Bracket metálico Roth .022"},
			locale.EN: {Name: "Metal bracket Roth .022"},
		},
	}
}

func newProduct(t *testing.T) *Product {
	t.Helper()
	p, err := NewProduct(details(), now)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func wantKind(t *testing.T, err error, k apperr.Kind, code string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Kind != k || e.Code != code {
		t.Fatalf("got %v, want kind %d code %s", err, k, code)
	}
}

func TestNewProductStartsAsDraft(t *testing.T) {
	p := newProduct(t)
	if p.Status != StatusDraft || p.ID.IsNil() {
		t.Fatalf("unexpected product: %+v", p)
	}
}

func TestNewProductValidation(t *testing.T) {
	d := details()
	d.Slug = "Bracket Roth"
	_, err := NewProduct(d, now)
	wantKind(t, err, apperr.KindInvalid, "catalog.slug_invalid")

	d = details()
	d.Texts = map[locale.Locale]Translation{locale.EN: {Name: "Only English"}}
	_, err = NewProduct(d, now)
	wantKind(t, err, apperr.KindInvalid, "catalog.name_required")

	d = details()
	d.Texts[locale.EN] = Translation{Description: "description without a name"}
	_, err = NewProduct(d, now)
	wantKind(t, err, apperr.KindInvalid, "catalog.name_required")
}

func TestNameFallsBackToSpanish(t *testing.T) {
	p := newProduct(t)
	if got := p.Name(locale.PTBR); got != "Bracket metálico Roth .022" {
		t.Fatalf("pt-br fallback = %q", got)
	}
	if got := p.Name(locale.EN); got != "Metal bracket Roth .022" {
		t.Fatalf("en = %q", got)
	}
}

func TestFirstVariantIsDefaultAndSKUIsNormalized(t *testing.T) {
	p := newProduct(t)
	v, err := p.AddVariant(VariantInput{SKU: " br-roth-022-11 ", Options: map[string]string{"tooth": "11"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !v.IsDefault || v.SKU != "BR-ROTH-022-11" {
		t.Fatalf("unexpected variant: %+v", v)
	}
	w, err := p.AddVariant(VariantInput{SKU: "BR-ROTH-022-12", Options: map[string]string{"tooth": "12"}}, now)
	if err != nil || w.IsDefault {
		t.Fatalf("second variant: %+v, %v", w, err)
	}
}

func TestVariantsMustDiffer(t *testing.T) {
	p := newProduct(t)
	if _, err := p.AddVariant(VariantInput{SKU: "A-1", Options: map[string]string{"tooth": "11"}}, now); err != nil {
		t.Fatal(err)
	}
	_, err := p.AddVariant(VariantInput{SKU: "a-1", Options: map[string]string{"tooth": "12"}}, now)
	wantKind(t, err, apperr.KindConflict, "catalog.sku_taken")

	_, err = p.AddVariant(VariantInput{SKU: "A-2", Options: map[string]string{"tooth": "11"}}, now)
	wantKind(t, err, apperr.KindConflict, "catalog.options_taken")

	_, err = p.AddVariant(VariantInput{SKU: "A-3", WeightG: -1}, now)
	wantKind(t, err, apperr.KindInvalid, "catalog.weight_invalid")
}

func TestPublishRequiresAVariant(t *testing.T) {
	p := newProduct(t)
	wantKind(t, p.ChangeStatus(StatusActive, now), apperr.KindConflict, "catalog.no_variants")

	if _, err := p.AddVariant(VariantInput{SKU: "A-1"}, now); err != nil {
		t.Fatal(err)
	}
	if err := p.ChangeStatus(StatusActive, now); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveVariantArchivesAndMovesDefault(t *testing.T) {
	p := newProduct(t)
	a, _ := p.AddVariant(VariantInput{SKU: "A-1", Options: map[string]string{"tooth": "11"}}, now)
	b, _ := p.AddVariant(VariantInput{SKU: "A-2", Options: map[string]string{"tooth": "12"}}, now)
	if err := p.ChangeStatus(StatusActive, now); err != nil {
		t.Fatal(err)
	}

	if err := p.RemoveVariant(a.ID, now); err != nil {
		t.Fatal(err)
	}
	if len(p.Variants) != 2 || !p.Variants[0].Archived {
		t.Fatal("variant should be archived, not deleted")
	}
	active := p.ActiveVariants()
	if len(active) != 1 || active[0].ID != b.ID || !active[0].IsDefault {
		t.Fatalf("remaining variant should become default: %+v", active)
	}

	wantKind(t, p.RemoveVariant(b.ID, now), apperr.KindConflict, "catalog.last_variant")
	wantKind(t, p.RemoveVariant(a.ID, now), apperr.KindNotFound, "catalog.variant_not_found")
}

func TestArchivedVariantFreesItsOptions(t *testing.T) {
	p := newProduct(t)
	a, _ := p.AddVariant(VariantInput{SKU: "A-1", Options: map[string]string{"tooth": "11"}}, now)
	_, _ = p.AddVariant(VariantInput{SKU: "A-2", Options: map[string]string{"tooth": "12"}}, now)
	if err := p.RemoveVariant(a.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AddVariant(VariantInput{SKU: "A-3", Options: map[string]string{"tooth": "11"}}, now); err != nil {
		t.Fatalf("options of an archived variant should be reusable: %v", err)
	}
}

func TestSetDefaultVariant(t *testing.T) {
	p := newProduct(t)
	_, _ = p.AddVariant(VariantInput{SKU: "A-1", Options: map[string]string{"tooth": "11"}}, now)
	b, _ := p.AddVariant(VariantInput{SKU: "A-2", Options: map[string]string{"tooth": "12"}}, now)
	if err := p.SetDefaultVariant(b.ID, now); err != nil {
		t.Fatal(err)
	}
	defaults := 0
	for _, v := range p.Variants {
		if v.IsDefault {
			defaults++
			if v.ID != b.ID {
				t.Fatal("wrong default")
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("exactly one default expected, got %d", defaults)
	}
	wantKind(t, p.SetDefaultVariant(id.New(), now), apperr.KindNotFound, "catalog.variant_not_found")
}

func TestMediaLimitAndRemoval(t *testing.T) {
	p := newProduct(t)
	var first Media
	for i := range maxMedia {
		m, err := p.AddMedia("products/x/img.webp", nil, now)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = m
		}
	}
	_, err := p.AddMedia("products/x/one-more.webp", nil, now)
	wantKind(t, err, apperr.KindInvalid, "catalog.too_many_images")

	if err := p.RemoveMedia(first.ID, now); err != nil {
		t.Fatal(err)
	}
	for i, m := range p.Media {
		if m.SortOrder != i {
			t.Fatalf("sort order not compacted: %+v", p.Media)
		}
	}
}

func TestCategoryTree(t *testing.T) {
	es := func(n string) map[locale.Locale]Translation {
		return map[locale.Locale]Translation{locale.ES: {Name: n}}
	}
	root, _ := NewCategory(CategoryDetails{Slug: "brackets", Texts: es("Brackets")}, now)
	metal, _ := NewCategory(CategoryDetails{Slug: "metalicos", ParentID: root.ID, Texts: es("Metálicos")}, now)
	ceramic, _ := NewCategory(CategoryDetails{Slug: "ceramicos", ParentID: root.ID, Texts: es("Cerámicos")}, now)
	wires, _ := NewCategory(CategoryDetails{Slug: "alambres", Texts: es("Alambres")}, now)
	all := []*Category{root, metal, ceramic, wires}

	if got := Descendants(all, root.ID); len(got) != 3 {
		t.Fatalf("brackets subtree = %v", got)
	}
	if !WouldCycle(all, root.ID, metal.ID) {
		t.Fatal("moving brackets under its own child must be a cycle")
	}
	if WouldCycle(all, metal.ID, wires.ID) {
		t.Fatal("moving metálicos under alambres is fine")
	}
}

func TestCheckOptions(t *testing.T) {
	tooth, _ := NewAttribute("tooth", locale.Text{locale.ES: "Pieza"}, 0)
	if _, err := tooth.AddValue("11", locale.Text{locale.ES: "11"}); err != nil {
		t.Fatal(err)
	}
	_, err := tooth.AddValue("11", locale.Text{locale.ES: "11"})
	wantKind(t, err, apperr.KindConflict, "catalog.attribute_value_taken")

	attrs := []*Attribute{tooth}
	if err := CheckOptions(attrs, map[string]string{"tooth": "11"}); err != nil {
		t.Fatal(err)
	}
	wantKind(t, CheckOptions(attrs, map[string]string{"tooth": "99"}), apperr.KindInvalid, "catalog.attribute_value_unknown")
	wantKind(t, CheckOptions(attrs, map[string]string{"slot": "022"}), apperr.KindInvalid, "catalog.attribute_unknown")
}
