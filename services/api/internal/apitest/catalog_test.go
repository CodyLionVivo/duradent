package apitest

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
)

func (c client) must(want int, method, path string, body any, out any) {
	c.t.Helper()
	var raw json.RawMessage
	if got := c.do(method, path, c.session == "", body, &raw); got != want { // admin token unless acting as a user
		c.t.Fatalf("%s %s: status %d, want %d: %s", method, path, got, want, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatal(err)
		}
	}
}

func TestCatalogAndPricingEndToEnd(t *testing.T) {
	c := newServer(t).client

	// Admin routes require the token.
	if got := c.do(http.MethodGet, "/v1/admin/catalog/products", false, nil, nil); got != http.StatusUnauthorized {
		t.Fatalf("anonymous admin call: %d", got)
	}

	c.must(201, "POST", "/v1/admin/catalog/attributes", map[string]any{"code": "slot", "names": map[string]string{"es": "Slot"}}, nil)
	for _, v := range []string{"018", "022"} {
		c.must(201, "POST", "/v1/admin/catalog/attributes/slot/values", map[string]any{"code": v, "labels": map[string]string{"es": "." + v}}, nil)
	}
	var cat struct{ ID string }
	c.must(201, "POST", "/v1/admin/catalog/categories", map[string]any{
		"slug": "brackets", "texts": map[string]any{"es": map[string]string{"name": "Brackets"}, "en": map[string]string{"name": "Brackets"}},
	}, &cat)

	var product struct {
		ID       string
		Variants []struct{ ID, SKU string }
	}
	c.must(201, "POST", "/v1/admin/catalog/products", map[string]any{
		"slug": "kit-roth", "brand": "Duradent", "category_id": cat.ID,
		"texts": map[string]any{"es": map[string]string{"name": "Kit Roth", "description": "Acero"}, "en": map[string]string{"name": "Roth kit"}},
	}, &product)

	// Validation and conflicts come back as problem+json with a stable code.
	var problem httpserver.Problem
	if got := c.do("POST", "/v1/admin/catalog/products", true, map[string]any{"slug": "Bad Slug", "texts": map[string]any{"es": map[string]string{"name": "x"}}}, &problem); got != 422 || problem.Code != "catalog.slug_invalid" {
		t.Fatalf("invalid slug: %d %+v", got, problem)
	}
	if got := c.do("POST", "/v1/admin/catalog/products", true, map[string]any{"slug": "kit-roth", "texts": map[string]any{"es": map[string]string{"name": "x"}}}, &problem); got != 409 {
		t.Fatalf("duplicate slug: %d %+v", got, problem)
	}

	// Drafts are invisible to the store.
	if got := c.do("GET", "/v1/catalog/products/kit-roth", false, nil, nil); got != 404 {
		t.Fatalf("draft visible: %d", got)
	}
	if got := c.do("POST", "/v1/admin/catalog/products/"+product.ID+"/status", true, map[string]string{"status": "active"}, &problem); got != 409 || problem.Code != "catalog.no_variants" {
		t.Fatalf("publishing without variants: %d %+v", got, problem)
	}

	for _, slot := range []string{"018", "022"} {
		c.must(201, "POST", "/v1/admin/catalog/products/"+product.ID+"/variants", map[string]any{"sku": "kit-roth-" + slot, "weight_g": 18, "options": map[string]string{"slot": slot}}, &product)
	}
	c.must(200, "POST", "/v1/admin/catalog/products/"+product.ID+"/status", map[string]string{"status": "active"}, nil)

	var up struct{ Key, UploadURL string }
	c.must(200, "POST", "/v1/admin/catalog/products/"+product.ID+"/images/uploads", map[string]string{"content_type": "image/webp"}, &up)
	c.must(201, "POST", "/v1/admin/catalog/products/"+product.ID+"/images", map[string]any{"key": up.Key, "alt": map[string]string{"es": "Kit completo"}}, nil)

	// Store views.
	var page struct {
		Items []struct {
			Slug, Name       string
			DefaultVariantID string `json:"default_variant_id"`
			Image            struct{ URL string }
		}
		Total int
	}
	if got := c.do("GET", "/v1/catalog/products?category=brackets&locale=en", false, nil, &page); got != 200 || page.Total != 1 || page.Items[0].Name != "Roth kit" || page.Items[0].Image.URL == "" {
		t.Fatalf("browse: %d %+v", got, page)
	}
	var detail struct {
		Name, Description string
		Category          struct{ Slug string }
		Options           []struct {
			Code   string
			Values []struct{ Code string }
		}
		Variants []struct{ ID, SKU string }
	}
	if got := c.do("GET", "/v1/catalog/products/kit-roth?locale=pt-br", false, nil, &detail); got != 200 {
		t.Fatalf("detail: %d", got)
	}
	if detail.Name != "Kit Roth" || detail.Description != "Acero" || detail.Category.Slug != "brackets" ||
		len(detail.Options) != 1 || len(detail.Options[0].Values) != 2 || len(detail.Variants) != 2 || detail.Variants[0].SKU != "KIT-ROTH-018" {
		t.Fatalf("detail (pt-br falls back to es): %+v", detail)
	}

	// Pricing: public list plus a confidential clinic list.
	var public, clinic struct{ ID string }
	c.must(201, "POST", "/v1/admin/pricing/price-lists", map[string]any{"code": "public", "name": "Público", "is_default": true}, &public)
	c.must(201, "POST", "/v1/admin/pricing/price-lists", map[string]any{"code": "clinica", "name": "Clínicas"}, &clinic)
	v018 := detail.Variants[0].ID
	c.must(200, "PUT", "/v1/admin/pricing/price-lists/"+public.ID+"/variants/"+v018, map[string]any{"prices": []map[string]any{
		{"currency": "PEN", "min_qty": 1, "unit_price": "85.00"},
		{"currency": "PEN", "min_qty": 10, "unit_price": "78.20"},
	}}, nil)
	if got := c.do("PUT", "/v1/admin/pricing/price-lists/"+public.ID+"/variants/"+v018, true, map[string]any{"prices": []map[string]any{
		{"currency": "USD", "min_qty": 1, "unit_price": "22.70"},
	}}, &problem); got != 422 || problem.Code != "pricing.currency_invalid" {
		t.Fatalf("prices are only in soles: %d %+v", got, problem)
	}
	c.must(200, "PUT", "/v1/admin/pricing/price-lists/"+clinic.ID+"/variants/"+v018, map[string]any{"prices": []map[string]any{
		{"currency": "PEN", "min_qty": 1, "unit_price": "70.00"},
	}}, nil)
	if got := c.do("PUT", "/v1/admin/pricing/price-lists/"+public.ID+"/variants/"+v018, true, map[string]any{"prices": []map[string]any{
		{"currency": "PEN", "min_qty": 1, "unit_price": "12.345"},
	}}, &problem); got != 422 {
		t.Fatalf("three decimals must be rejected: %d", got)
	}

	var quotes struct {
		Currency string
		Items    []struct {
			VariantID string `json:"variant_id"`
			UnitPrice string `json:"unit_price"`
			Tiers     []struct {
				MinQty int `json:"min_qty"`
			}
		}
	}
	path := "/v1/pricing/quotes?currency=PEN&variant_id=" + v018 + "," + detail.Variants[1].ID
	if got := c.do("GET", path, false, nil, &quotes); got != 200 || len(quotes.Items) != 1 || quotes.Items[0].UnitPrice != "85.00" || len(quotes.Items[0].Tiers) != 2 {
		t.Fatalf("public quote (unpriced variant omitted): %d %+v", got, quotes)
	}
	// The clinic list cannot be requested by query parameter.
	_ = c.do("GET", path+"&price_list=clinica", false, nil, &quotes)
	if quotes.Items[0].UnitPrice != "85.00" {
		t.Fatalf("confidential list leaked through the public endpoint: %+v", quotes)
	}
	if got := c.do("GET", "/v1/pricing/quotes?currency=BRL&variant_id="+v018, false, nil, &problem); got != 422 {
		t.Fatalf("BRL: %d", got)
	}

	// Archiving a variant hides it from the store.
	c.must(200, "DELETE", "/v1/admin/catalog/products/"+product.ID+"/variants/"+v018, nil, nil)
	_ = c.do("GET", "/v1/catalog/products/kit-roth", false, nil, &detail)
	if len(detail.Variants) != 1 || detail.Variants[0].SKU != "KIT-ROTH-022" {
		t.Fatalf("archived variant still listed: %+v", detail.Variants)
	}
}
