// Package domain holds the catalog model: products sold in variants (a bracket
// in Roth .022 for tooth 11 is a variant with its own SKU), categories and the
// attributes variants are described by.
package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

type Status string

const (
	StatusDraft    Status = "draft"    // being edited, not visible in the store
	StatusActive   Status = "active"   // visible and purchasable
	StatusArchived Status = "archived" // withdrawn; kept for order history
)

func ParseStatus(s string) (Status, error) {
	switch st := Status(s); st {
	case StatusDraft, StatusActive, StatusArchived:
		return st, nil
	}
	return "", apperr.Invalid("catalog.status_invalid", "status", "unknown status")
}

const (
	maxMedia       = 12
	maxVariants    = 500
	maxNameLen     = 200
	maxDescLen     = 5000
	maxBrandLen    = 80
	maxWeightGrams = 100_000
)

var (
	slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	skuRe  = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{1,63}$`)
)

// Translation is the localized copy of a product or category.
type Translation struct {
	Name        string
	Description string
}

// Details are the editable, non-variant fields of a product.
type Details struct {
	Slug       string
	Brand      string
	CategoryID id.ID // id.Nil: uncategorized
	Texts      map[locale.Locale]Translation
}

type Product struct {
	ID         id.ID
	Slug       string
	Brand      string
	CategoryID id.ID
	Status     Status
	Texts      map[locale.Locale]Translation
	Variants   []Variant // includes archived variants; see ActiveVariants
	Media      []Media
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Variant is the purchasable unit. Options maps attribute code → value code,
// e.g. {"prescription": "roth", "slot": "022", "tooth": "11"}.
type Variant struct {
	ID        id.ID
	SKU       string
	Barcode   string
	WeightG   int
	IsDefault bool
	Archived  bool
	Options   map[string]string
}

type VariantInput struct {
	SKU     string
	Barcode string
	WeightG int
	Options map[string]string
}

type Media struct {
	ID        id.ID
	Key       string // object key in the media bucket
	Alt       locale.Text
	SortOrder int
}

func NewProduct(d Details, now time.Time) (*Product, error) {
	p := &Product{ID: id.New(), Status: StatusDraft, CreatedAt: now, UpdatedAt: now}
	if err := p.UpdateDetails(d, now); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Product) UpdateDetails(d Details, now time.Time) error {
	d.Slug = strings.TrimSpace(d.Slug)
	d.Brand = strings.TrimSpace(d.Brand)
	if !slugRe.MatchString(d.Slug) || len(d.Slug) > 120 {
		return apperr.Invalid("catalog.slug_invalid", "slug", "use lowercase letters, digits and hyphens")
	}
	if utf8.RuneCountInString(d.Brand) > maxBrandLen {
		return apperr.Invalid("catalog.brand_too_long", "brand", "brand is too long")
	}
	texts, err := cleanTexts(d.Texts)
	if err != nil {
		return err
	}
	p.Slug, p.Brand, p.CategoryID, p.Texts = d.Slug, d.Brand, d.CategoryID, texts
	p.UpdatedAt = now
	return nil
}

func cleanTexts(in map[locale.Locale]Translation) (map[locale.Locale]Translation, error) {
	out := make(map[locale.Locale]Translation, len(in))
	for l, t := range in {
		if _, err := locale.Parse(string(l)); err != nil {
			return nil, apperr.Invalid("catalog.locale_invalid", "texts", "unsupported locale "+string(l))
		}
		t.Name = strings.TrimSpace(t.Name)
		t.Description = strings.TrimSpace(t.Description)
		if t.Name == "" && t.Description == "" {
			continue
		}
		if t.Name == "" {
			return nil, apperr.Invalid("catalog.name_required", "texts."+string(l)+".name", "a translation needs a name")
		}
		if utf8.RuneCountInString(t.Name) > maxNameLen {
			return nil, apperr.Invalid("catalog.name_too_long", "texts."+string(l)+".name", "name is too long")
		}
		if utf8.RuneCountInString(t.Description) > maxDescLen {
			return nil, apperr.Invalid("catalog.description_too_long", "texts."+string(l)+".description", "description is too long")
		}
		out[l] = t
	}
	if out[locale.Default].Name == "" {
		return nil, apperr.Invalid("catalog.name_required", "texts.es.name", "a Spanish name is required")
	}
	return out, nil
}

// Name returns the product name in l, falling back to the default locale.
func (p *Product) Name(l locale.Locale) string {
	if t, ok := p.Texts[l]; ok && t.Name != "" {
		return t.Name
	}
	return p.Texts[locale.Default].Name
}

func (p *Product) Description(l locale.Locale) string {
	if t, ok := p.Texts[l]; ok && t.Description != "" {
		return t.Description
	}
	return p.Texts[locale.Default].Description
}

func (p *Product) ActiveVariants() []Variant {
	out := make([]Variant, 0, len(p.Variants))
	for _, v := range p.Variants {
		if !v.Archived {
			out = append(out, v)
		}
	}
	return out
}

// AddVariant validates the input against the product's other active variants.
// Global SKU uniqueness is enforced by the repository.
func (p *Product) AddVariant(in VariantInput, now time.Time) (Variant, error) {
	if len(p.Variants) >= maxVariants {
		return Variant{}, apperr.Invalid("catalog.too_many_variants", "variants", "too many variants")
	}
	v := Variant{ID: id.New()}
	if err := p.applyVariant(&v, in); err != nil {
		return Variant{}, err
	}
	v.IsDefault = len(p.ActiveVariants()) == 0
	p.Variants = append(p.Variants, v)
	p.UpdatedAt = now
	return v, nil
}

func (p *Product) UpdateVariant(variantID id.ID, in VariantInput, now time.Time) error {
	i := p.variantIndex(variantID)
	if i < 0 || p.Variants[i].Archived {
		return apperr.NotFound("catalog.variant_not_found", "variant not found")
	}
	v := p.Variants[i]
	if err := p.applyVariant(&v, in); err != nil {
		return err
	}
	p.Variants[i] = v
	p.UpdatedAt = now
	return nil
}

// RemoveVariant archives the variant instead of deleting it: prices, stock
// and past orders keep pointing at a valid row.
func (p *Product) RemoveVariant(variantID id.ID, now time.Time) error {
	i := p.variantIndex(variantID)
	if i < 0 || p.Variants[i].Archived {
		return apperr.NotFound("catalog.variant_not_found", "variant not found")
	}
	if p.Status == StatusActive && len(p.ActiveVariants()) == 1 {
		return apperr.Conflict("catalog.last_variant", "variants", "an active product needs at least one variant")
	}
	wasDefault := p.Variants[i].IsDefault
	p.Variants[i].Archived = true
	p.Variants[i].IsDefault = false
	if wasDefault {
		for j := range p.Variants {
			if !p.Variants[j].Archived {
				p.Variants[j].IsDefault = true
				break
			}
		}
	}
	p.UpdatedAt = now
	return nil
}

func (p *Product) SetDefaultVariant(variantID id.ID, now time.Time) error {
	i := p.variantIndex(variantID)
	if i < 0 || p.Variants[i].Archived {
		return apperr.NotFound("catalog.variant_not_found", "variant not found")
	}
	for j := range p.Variants {
		p.Variants[j].IsDefault = j == i
	}
	p.UpdatedAt = now
	return nil
}

func (p *Product) applyVariant(v *Variant, in VariantInput) error {
	sku := strings.ToUpper(strings.TrimSpace(in.SKU))
	if !skuRe.MatchString(sku) {
		return apperr.Invalid("catalog.sku_invalid", "sku", "SKU must be 2-64 letters, digits, '.', '_' or '-'")
	}
	if in.WeightG < 0 || in.WeightG > maxWeightGrams {
		return apperr.Invalid("catalog.weight_invalid", "weight_g", "weight must be between 0 and 100000 g")
	}
	opts := make(map[string]string, len(in.Options))
	for k, val := range in.Options {
		k, val = strings.TrimSpace(k), strings.TrimSpace(val)
		if k == "" || val == "" {
			continue
		}
		opts[k] = val
	}
	for _, other := range p.ActiveVariants() {
		if other.ID == v.ID {
			continue
		}
		if other.SKU == sku {
			return apperr.Conflict("catalog.sku_taken", "sku", "SKU already used by another variant")
		}
		if sameOptions(other.Options, opts) {
			return apperr.Conflict("catalog.options_taken", "options", "another variant has the same options")
		}
	}
	v.SKU, v.Barcode, v.WeightG, v.Options = sku, strings.TrimSpace(in.Barcode), in.WeightG, opts
	return nil
}

func sameOptions(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func (p *Product) variantIndex(variantID id.ID) int {
	for i, v := range p.Variants {
		if v.ID == variantID {
			return i
		}
	}
	return -1
}

// ChangeStatus moves the product through draft → active → archived. Any
// state can go back to draft for editing.
func (p *Product) ChangeStatus(to Status, now time.Time) error {
	if to == p.Status {
		return nil
	}
	if to == StatusActive && len(p.ActiveVariants()) == 0 {
		return apperr.Conflict("catalog.no_variants", "status", "add at least one variant before publishing")
	}
	p.Status = to
	p.UpdatedAt = now
	return nil
}

func (p *Product) AddMedia(key string, alt locale.Text, now time.Time) (Media, error) {
	if len(p.Media) >= maxMedia {
		return Media{}, apperr.Invalid("catalog.too_many_images", "media", "a product can have at most 12 images")
	}
	if strings.TrimSpace(key) == "" {
		return Media{}, apperr.Invalid("catalog.media_key_required", "key", "missing object key")
	}
	m := Media{ID: id.New(), Key: key, Alt: alt, SortOrder: len(p.Media)}
	p.Media = append(p.Media, m)
	p.UpdatedAt = now
	return m, nil
}

func (p *Product) RemoveMedia(mediaID id.ID, now time.Time) error {
	for i, m := range p.Media {
		if m.ID == mediaID {
			p.Media = append(p.Media[:i], p.Media[i+1:]...)
			for j := range p.Media {
				p.Media[j].SortOrder = j
			}
			p.UpdatedAt = now
			return nil
		}
	}
	return apperr.NotFound("catalog.media_not_found", "image not found")
}

// ValidSlug reports whether s can be used as a product or category slug.
func ValidSlug(s string) bool { return slugRe.MatchString(s) && len(s) <= 120 }
