// Package http exposes the catalog over HTTP: localized, read-only routes for
// the store under /v1/catalog and full editing under /v1/admin/catalog.
package http

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
)

type Handler struct {
	svc *application.Service
	log *slog.Logger
}

// Register mounts the routes. admin wraps every admin handler (auth).
func Register(mux *http.ServeMux, svc *application.Service, log *slog.Logger, admin func(http.Handler) http.Handler) {
	h := &Handler{svc: svc, log: log}

	mux.HandleFunc("GET /v1/catalog/categories", h.listCategories)
	mux.HandleFunc("GET /v1/catalog/attributes", h.listAttributes)
	mux.HandleFunc("GET /v1/catalog/products", h.browseProducts)
	mux.HandleFunc("GET /v1/catalog/products/{slug}", h.productBySlug)

	a := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, admin(fn)) }
	a("GET /v1/admin/catalog/products", h.adminListProducts)
	a("POST /v1/admin/catalog/products", h.adminCreateProduct)
	a("GET /v1/admin/catalog/products/{id}", h.adminGetProduct)
	a("PUT /v1/admin/catalog/products/{id}", h.adminUpdateProduct)
	a("POST /v1/admin/catalog/products/{id}/status", h.adminChangeStatus)
	a("POST /v1/admin/catalog/products/{id}/variants", h.adminAddVariant)
	a("PUT /v1/admin/catalog/products/{id}/variants/{variant}", h.adminUpdateVariant)
	a("DELETE /v1/admin/catalog/products/{id}/variants/{variant}", h.adminRemoveVariant)
	a("POST /v1/admin/catalog/products/{id}/variants/{variant}/default", h.adminSetDefaultVariant)
	a("POST /v1/admin/catalog/products/{id}/images/uploads", h.adminRequestUpload)
	a("POST /v1/admin/catalog/products/{id}/images", h.adminAttachImage)
	a("DELETE /v1/admin/catalog/products/{id}/images/{image}", h.adminRemoveImage)
	a("GET /v1/admin/catalog/categories", h.adminListCategories)
	a("POST /v1/admin/catalog/categories", h.adminCreateCategory)
	a("PUT /v1/admin/catalog/categories/{id}", h.adminUpdateCategory)
	a("GET /v1/admin/catalog/attributes", h.adminListAttributes)
	a("POST /v1/admin/catalog/attributes", h.adminCreateAttribute)
	a("POST /v1/admin/catalog/attributes/{code}/values", h.adminAddAttributeValue)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpserver.Error(w, r, h.log, err)
}

func localeOf(r *http.Request) locale.Locale {
	return locale.ParseOrDefault(r.URL.Query().Get("locale"))
}

// --- Storefront DTOs ---------------------------------------------------------------

type imageDTO struct {
	ID  id.ID  `json:"id"`
	URL string `json:"url"`
	Alt string `json:"alt"`
}

type productSummaryDTO struct {
	ID               id.ID     `json:"id"`
	Slug             string    `json:"slug"`
	Brand            string    `json:"brand"`
	Name             string    `json:"name"`
	CategoryID       *id.ID    `json:"category_id"`
	Image            *imageDTO `json:"image"`
	DefaultVariantID *id.ID    `json:"default_variant_id"`
	VariantCount     int       `json:"variant_count"`
}

type optionValueDTO struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

type optionDTO struct {
	Code   string           `json:"code"`
	Name   string           `json:"name"`
	Values []optionValueDTO `json:"values"`
}

type variantDTO struct {
	ID        id.ID             `json:"id"`
	SKU       string            `json:"sku"`
	WeightG   int               `json:"weight_g"`
	IsDefault bool              `json:"is_default"`
	Options   map[string]string `json:"options"`
}

type categoryRefDTO struct {
	ID   id.ID  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type productDetailDTO struct {
	ID          id.ID           `json:"id"`
	Slug        string          `json:"slug"`
	Brand       string          `json:"brand"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Category    *categoryRefDTO `json:"category"`
	Images      []imageDTO      `json:"images"`
	Options     []optionDTO     `json:"options"`
	Variants    []variantDTO    `json:"variants"`
}

type categoryDTO struct {
	ID          id.ID  `json:"id"`
	Slug        string `json:"slug"`
	ParentID    *id.ID `json:"parent_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	SortOrder   int    `json:"sort_order"`
}

type pageDTO[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

func optional(v id.ID) *id.ID {
	if v.IsNil() {
		return nil
	}
	return &v
}

func (h *Handler) images(p *domain.Product, l locale.Locale) []imageDTO {
	out := make([]imageDTO, 0, len(p.Media))
	for _, m := range p.Media {
		alt := m.Alt.In(l)
		if alt == "" {
			alt = p.Name(l)
		}
		out = append(out, imageDTO{ID: m.ID, URL: h.svc.MediaURL(m.Key), Alt: alt})
	}
	return out
}

func (h *Handler) summary(p *domain.Product, l locale.Locale) productSummaryDTO {
	s := productSummaryDTO{
		ID: p.ID, Slug: p.Slug, Brand: p.Brand, Name: p.Name(l), CategoryID: optional(p.CategoryID),
	}
	if imgs := h.images(p, l); len(imgs) > 0 {
		s.Image = &imgs[0]
	}
	for _, v := range p.ActiveVariants() {
		s.VariantCount++
		if v.IsDefault {
			s.DefaultVariantID = optional(v.ID)
		}
	}
	return s
}

func categoryDTOs(cs []*domain.Category, l locale.Locale) []categoryDTO {
	out := make([]categoryDTO, 0, len(cs))
	for _, c := range cs {
		desc := c.Texts[l].Description
		if desc == "" {
			desc = c.Texts[locale.Default].Description
		}
		out = append(out, categoryDTO{ID: c.ID, Slug: c.Slug, ParentID: optional(c.ParentID), Name: c.Name(l), Description: desc, SortOrder: c.SortOrder})
	}
	return out
}

// --- Storefront handlers --------------------------------------------------------------

func (h *Handler) listCategories(w http.ResponseWriter, r *http.Request) {
	cs, err := h.svc.Categories(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	httpserver.JSON(w, http.StatusOK, categoryDTOs(cs, localeOf(r)))
}

func (h *Handler) listAttributes(w http.ResponseWriter, r *http.Request) {
	attrs, err := h.svc.Attributes(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	l := localeOf(r)
	out := make([]optionDTO, 0, len(attrs))
	for _, a := range attrs {
		o := optionDTO{Code: a.Code, Name: a.Names.In(l)}
		for _, v := range a.Values {
			o.Values = append(o.Values, optionValueDTO{Code: v.Code, Label: v.Labels.In(l)})
		}
		out = append(out, o)
	}
	httpserver.JSON(w, http.StatusOK, out)
}

func (h *Handler) browseProducts(w http.ResponseWriter, r *http.Request) {
	page, err := httpserver.QueryInt(r, "page", 1, 10_000)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	size, err := httpserver.QueryInt(r, "page_size", 24, 100)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	l := localeOf(r)
	q := r.URL.Query()
	// attr=slot:022&attr=arch:upper — values of one attribute are alternatives.
	attrs := map[string][]string{}
	for _, raw := range q["attr"] {
		if code, value, ok := strings.Cut(raw, ":"); ok {
			attrs[code] = append(attrs[code], value)
		}
	}
	ctx := r.Context()
	res, err := h.svc.BrowseProducts(ctx, application.BrowseQuery{
		CategorySlug: q.Get("category"), Query: q.Get("q"), Locale: l,
		Brands: q["brand"], Attributes: attrs, Sort: ports.ProductSort(q.Get("sort")),
		Page: application.Page{Limit: size, Offset: (page - 1) * size},
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	facets, err := h.facets(ctx, res.Facets, q["brand"], attrs, l)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]productSummaryDTO, 0, len(res.Products))
	for _, p := range res.Products {
		items = append(items, h.summary(p, l))
	}
	httpserver.JSON(w, http.StatusOK, browseDTO{
		pageDTO: pageDTO[productSummaryDTO]{Items: items, Total: res.Total, Page: page, PageSize: size},
		Facets:  facets,
	})
}

type facetValueDTO struct {
	Value    string `json:"value"`
	Label    string `json:"label"`
	Count    int    `json:"count"`
	Selected bool   `json:"selected"`
}

type attributeFacetDTO struct {
	Code   string          `json:"code"`
	Name   string          `json:"name"`
	Values []facetValueDTO `json:"values"`
}

type categoryFacetDTO struct {
	Slug  string `json:"slug"`
	Count int    `json:"count"` // products directly in the category
}

type facetsDTO struct {
	Brands     []facetValueDTO     `json:"brands"`
	Categories []categoryFacetDTO  `json:"categories"`
	Attributes []attributeFacetDTO `json:"attributes"`
}

type browseDTO struct {
	pageDTO[productSummaryDTO]
	Facets facetsDTO `json:"facets"`
}

// facets localizes the counts and keeps every selected value listed, even
// when it currently matches nothing, so the visitor can unselect it.
func (h *Handler) facets(ctx context.Context, f ports.Facets, brands []string, attrs map[string][]string, l locale.Locale) (facetsDTO, error) {
	out := facetsDTO{Brands: []facetValueDTO{}, Categories: []categoryFacetDTO{}, Attributes: []attributeFacetDTO{}}

	counted := map[string]bool{}
	for _, b := range f.Brands {
		counted[b.Value] = true
		out.Brands = append(out.Brands, facetValueDTO{Value: b.Value, Label: b.Value, Count: b.Count, Selected: slices.Contains(brands, b.Value)})
	}
	for _, b := range brands {
		if !counted[b] && b != "" {
			out.Brands = append(out.Brands, facetValueDTO{Value: b, Label: b, Selected: true})
		}
	}

	cats, err := h.svc.Categories(ctx)
	if err != nil {
		return out, err
	}
	for _, c := range cats {
		if n := f.Categories[c.ID]; n > 0 {
			out.Categories = append(out.Categories, categoryFacetDTO{Slug: c.Slug, Count: n})
		}
	}

	defs, err := h.svc.Attributes(ctx)
	if err != nil {
		return out, err
	}
	slices.SortFunc(defs, func(a, b *domain.Attribute) int { return a.SortOrder - b.SortOrder })
	for _, a := range defs {
		counts := map[string]int{}
		for _, c := range f.Attributes[a.Code] {
			counts[c.Value] = c.Count
		}
		facet := attributeFacetDTO{Code: a.Code, Name: a.Names.In(l), Values: []facetValueDTO{}}
		values := slices.Clone(a.Values)
		slices.SortFunc(values, func(x, y domain.AttributeValue) int { return x.SortOrder - y.SortOrder })
		for _, v := range values {
			selected := slices.Contains(attrs[a.Code], v.Code)
			if counts[v.Code] == 0 && !selected {
				continue
			}
			facet.Values = append(facet.Values, facetValueDTO{Value: v.Code, Label: v.Labels.In(l), Count: counts[v.Code], Selected: selected})
		}
		if len(facet.Values) > 0 {
			out.Attributes = append(out.Attributes, facet)
		}
	}
	return out, nil
}

func (h *Handler) productBySlug(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := h.svc.ProductForStore(ctx, r.PathValue("slug"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	l := localeOf(r)
	out := productDetailDTO{
		ID: p.ID, Slug: p.Slug, Brand: p.Brand, Name: p.Name(l), Description: p.Description(l),
		Images: h.images(p, l), Options: []optionDTO{}, Variants: []variantDTO{},
	}
	if !p.CategoryID.IsNil() {
		cs, err := h.svc.Categories(ctx)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		for _, c := range cs {
			if c.ID == p.CategoryID {
				out.Category = &categoryRefDTO{ID: c.ID, Slug: c.Slug, Name: c.Name(l)}
			}
		}
	}

	used := map[string]map[string]bool{}
	for _, v := range p.ActiveVariants() {
		out.Variants = append(out.Variants, variantDTO{ID: v.ID, SKU: v.SKU, WeightG: v.WeightG, IsDefault: v.IsDefault, Options: v.Options})
		for k, val := range v.Options {
			if used[k] == nil {
				used[k] = map[string]bool{}
			}
			used[k][val] = true
		}
	}
	// Options lists only the attributes and values this product's variants use,
	// in the admin-defined order, so the variant picker never offers dead ends.
	attrs, err := h.svc.Attributes(ctx)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	for _, a := range attrs {
		if used[a.Code] == nil {
			continue
		}
		o := optionDTO{Code: a.Code, Name: a.Names.In(l)}
		for _, v := range a.Values {
			if used[a.Code][v.Code] {
				o.Values = append(o.Values, optionValueDTO{Code: v.Code, Label: v.Labels.In(l)})
			}
		}
		out.Options = append(out.Options, o)
	}
	httpserver.JSON(w, http.StatusOK, out)
}

// --- Admin DTOs ------------------------------------------------------------------------

type textDTO struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type adminVariantDTO struct {
	ID        id.ID             `json:"id"`
	SKU       string            `json:"sku"`
	Barcode   string            `json:"barcode"`
	WeightG   int               `json:"weight_g"`
	IsDefault bool              `json:"is_default"`
	Archived  bool              `json:"archived"`
	Options   map[string]string `json:"options"`
}

type adminImageDTO struct {
	ID  id.ID             `json:"id"`
	Key string            `json:"key"`
	URL string            `json:"url"`
	Alt map[string]string `json:"alt"`
}

type adminProductDTO struct {
	ID         id.ID              `json:"id"`
	Slug       string             `json:"slug"`
	Brand      string             `json:"brand"`
	Status     domain.Status      `json:"status"`
	CategoryID *id.ID             `json:"category_id"`
	Texts      map[string]textDTO `json:"texts"`
	Variants   []adminVariantDTO  `json:"variants"`
	Images     []adminImageDTO    `json:"images"`
	CreatedAt  time.Time          `json:"created_at"`
	UpdatedAt  time.Time          `json:"updated_at"`
}

type adminProductRowDTO struct {
	ID           id.ID         `json:"id"`
	Slug         string        `json:"slug"`
	Name         string        `json:"name"`
	Brand        string        `json:"brand"`
	Status       domain.Status `json:"status"`
	VariantCount int           `json:"variant_count"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

type adminCategoryDTO struct {
	ID        id.ID              `json:"id"`
	Slug      string             `json:"slug"`
	ParentID  *id.ID             `json:"parent_id"`
	SortOrder int                `json:"sort_order"`
	Texts     map[string]textDTO `json:"texts"`
}

type adminAttributeValueDTO struct {
	Code   string            `json:"code"`
	Labels map[string]string `json:"labels"`
}

type adminAttributeDTO struct {
	Code      string                   `json:"code"`
	Names     map[string]string        `json:"names"`
	SortOrder int                      `json:"sort_order"`
	Values    []adminAttributeValueDTO `json:"values"`
}

func textsDTO(texts map[locale.Locale]domain.Translation) map[string]textDTO {
	out := make(map[string]textDTO, len(texts))
	for l, t := range texts {
		out[string(l)] = textDTO{Name: t.Name, Description: t.Description}
	}
	return out
}

func textsFrom(in map[string]textDTO) map[locale.Locale]domain.Translation {
	out := make(map[locale.Locale]domain.Translation, len(in))
	for l, t := range in {
		out[locale.Locale(l)] = domain.Translation{Name: t.Name, Description: t.Description}
	}
	return out
}

func localized(t locale.Text) map[string]string {
	out := make(map[string]string, len(t))
	for l, v := range t {
		out[string(l)] = v
	}
	return out
}

func textFrom(in map[string]string) locale.Text {
	out := make(locale.Text, len(in))
	for l, v := range in {
		if pl, err := locale.Parse(l); err == nil && v != "" {
			out[pl] = v
		}
	}
	return out
}

func (h *Handler) adminProduct(p *domain.Product) adminProductDTO {
	out := adminProductDTO{
		ID: p.ID, Slug: p.Slug, Brand: p.Brand, Status: p.Status, CategoryID: optional(p.CategoryID),
		Texts: textsDTO(p.Texts), Variants: []adminVariantDTO{}, Images: []adminImageDTO{},
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	for _, v := range p.Variants {
		out.Variants = append(out.Variants, adminVariantDTO{
			ID: v.ID, SKU: v.SKU, Barcode: v.Barcode, WeightG: v.WeightG, IsDefault: v.IsDefault, Archived: v.Archived, Options: v.Options,
		})
	}
	for _, m := range p.Media {
		out.Images = append(out.Images, adminImageDTO{ID: m.ID, Key: m.Key, URL: h.svc.MediaURL(m.Key), Alt: localized(m.Alt)})
	}
	return out
}

// --- Admin handlers ----------------------------------------------------------------------

func (h *Handler) adminListProducts(w http.ResponseWriter, r *http.Request) {
	page, err := httpserver.QueryInt(r, "page", 1, 10_000)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	size, err := httpserver.QueryInt(r, "page_size", 50, 100)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	q := r.URL.Query()
	var statuses []domain.Status
	if s := q.Get("status"); s != "" {
		st, err := domain.ParseStatus(s)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		statuses = []domain.Status{st}
	}
	ps, total, err := h.svc.AdminProducts(r.Context(), application.AdminProductQuery{
		Statuses: statuses, Query: q.Get("q"), Page: application.Page{Limit: size, Offset: (page - 1) * size},
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]adminProductRowDTO, 0, len(ps))
	for _, p := range ps {
		items = append(items, adminProductRowDTO{
			ID: p.ID, Slug: p.Slug, Name: p.Name(locale.Default), Brand: p.Brand, Status: p.Status,
			VariantCount: len(p.ActiveVariants()), UpdatedAt: p.UpdatedAt,
		})
	}
	httpserver.JSON(w, http.StatusOK, pageDTO[adminProductRowDTO]{Items: items, Total: total, Page: page, PageSize: size})
}

type productInput struct {
	Slug       string             `json:"slug"`
	Brand      string             `json:"brand"`
	CategoryID *id.ID             `json:"category_id"`
	Texts      map[string]textDTO `json:"texts"`
}

func (in productInput) details() domain.Details {
	d := domain.Details{Slug: in.Slug, Brand: in.Brand, Texts: textsFrom(in.Texts)}
	if in.CategoryID != nil {
		d.CategoryID = *in.CategoryID
	}
	return d
}

func (h *Handler) adminCreateProduct(w http.ResponseWriter, r *http.Request) {
	var in productInput
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.svc.CreateProduct(r.Context(), in.details())
	h.respondProduct(w, r, http.StatusCreated, p, err)
}

func (h *Handler) adminGetProduct(w http.ResponseWriter, r *http.Request) {
	pid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.svc.Product(r.Context(), pid)
	h.respondProduct(w, r, http.StatusOK, p, err)
}

func (h *Handler) adminUpdateProduct(w http.ResponseWriter, r *http.Request) {
	pid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in productInput
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.svc.UpdateProduct(r.Context(), pid, in.details())
	h.respondProduct(w, r, http.StatusOK, p, err)
}

func (h *Handler) adminChangeStatus(w http.ResponseWriter, r *http.Request) {
	pid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	st, err := domain.ParseStatus(in.Status)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.svc.ChangeStatus(r.Context(), pid, st)
	h.respondProduct(w, r, http.StatusOK, p, err)
}

type variantInput struct {
	SKU     string            `json:"sku"`
	Barcode string            `json:"barcode"`
	WeightG int               `json:"weight_g"`
	Options map[string]string `json:"options"`
}

func (in variantInput) domain() domain.VariantInput {
	return domain.VariantInput{SKU: in.SKU, Barcode: in.Barcode, WeightG: in.WeightG, Options: in.Options}
}

func (h *Handler) adminAddVariant(w http.ResponseWriter, r *http.Request) {
	pid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in variantInput
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.svc.AddVariant(r.Context(), pid, in.domain())
	h.respondProduct(w, r, http.StatusCreated, p, err)
}

func (h *Handler) adminUpdateVariant(w http.ResponseWriter, r *http.Request) {
	pid, vid, err := productAndChild(r, "variant")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in variantInput
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.svc.UpdateVariant(r.Context(), pid, vid, in.domain())
	h.respondProduct(w, r, http.StatusOK, p, err)
}

func (h *Handler) adminRemoveVariant(w http.ResponseWriter, r *http.Request) {
	pid, vid, err := productAndChild(r, "variant")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.svc.RemoveVariant(r.Context(), pid, vid)
	h.respondProduct(w, r, http.StatusOK, p, err)
}

func (h *Handler) adminSetDefaultVariant(w http.ResponseWriter, r *http.Request) {
	pid, vid, err := productAndChild(r, "variant")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.svc.SetDefaultVariant(r.Context(), pid, vid)
	h.respondProduct(w, r, http.StatusOK, p, err)
}

func (h *Handler) adminRequestUpload(w http.ResponseWriter, r *http.Request) {
	pid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		ContentType string `json:"content_type"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	key, up, err := h.svc.RequestImageUpload(r.Context(), pid, in.ContentType)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"key": key, "upload_url": up.URL, "method": up.Method, "headers": up.Headers,
	})
}

func (h *Handler) adminAttachImage(w http.ResponseWriter, r *http.Request) {
	pid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		Key string            `json:"key"`
		Alt map[string]string `json:"alt"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.svc.AttachImage(r.Context(), pid, in.Key, textFrom(in.Alt))
	h.respondProduct(w, r, http.StatusCreated, p, err)
}

func (h *Handler) adminRemoveImage(w http.ResponseWriter, r *http.Request) {
	pid, mid, err := productAndChild(r, "image")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	p, err := h.svc.RemoveImage(r.Context(), pid, mid)
	h.respondProduct(w, r, http.StatusOK, p, err)
}

func productAndChild(r *http.Request, child string) (id.ID, id.ID, error) {
	pid, err := httpserver.PathID(r, "id")
	if err != nil {
		return id.Nil, id.Nil, err
	}
	cid, err := httpserver.PathID(r, child)
	return pid, cid, err
}

func (h *Handler) respondProduct(w http.ResponseWriter, r *http.Request, status int, p *domain.Product, err error) {
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, status, h.adminProduct(p))
}

func (h *Handler) adminListCategories(w http.ResponseWriter, r *http.Request) {
	cs, err := h.svc.Categories(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]adminCategoryDTO, 0, len(cs))
	for _, c := range cs {
		out = append(out, adminCategoryDTO{ID: c.ID, Slug: c.Slug, ParentID: optional(c.ParentID), SortOrder: c.SortOrder, Texts: textsDTO(c.Texts)})
	}
	httpserver.JSON(w, http.StatusOK, out)
}

type categoryInput struct {
	Slug      string             `json:"slug"`
	ParentID  *id.ID             `json:"parent_id"`
	SortOrder int                `json:"sort_order"`
	Texts     map[string]textDTO `json:"texts"`
}

func (in categoryInput) details() domain.CategoryDetails {
	d := domain.CategoryDetails{Slug: in.Slug, SortOrder: in.SortOrder, Texts: textsFrom(in.Texts)}
	if in.ParentID != nil {
		d.ParentID = *in.ParentID
	}
	return d
}

func (h *Handler) adminCreateCategory(w http.ResponseWriter, r *http.Request) {
	var in categoryInput
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	c, err := h.svc.CreateCategory(r.Context(), in.details())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, adminCategoryDTO{ID: c.ID, Slug: c.Slug, ParentID: optional(c.ParentID), SortOrder: c.SortOrder, Texts: textsDTO(c.Texts)})
}

func (h *Handler) adminUpdateCategory(w http.ResponseWriter, r *http.Request) {
	cid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in categoryInput
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	c, err := h.svc.UpdateCategory(r.Context(), cid, in.details())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, adminCategoryDTO{ID: c.ID, Slug: c.Slug, ParentID: optional(c.ParentID), SortOrder: c.SortOrder, Texts: textsDTO(c.Texts)})
}

func attributeDTO(a *domain.Attribute) adminAttributeDTO {
	out := adminAttributeDTO{Code: a.Code, Names: localized(a.Names), SortOrder: a.SortOrder, Values: []adminAttributeValueDTO{}}
	for _, v := range a.Values {
		out.Values = append(out.Values, adminAttributeValueDTO{Code: v.Code, Labels: localized(v.Labels)})
	}
	return out
}

func (h *Handler) adminListAttributes(w http.ResponseWriter, r *http.Request) {
	attrs, err := h.svc.Attributes(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]adminAttributeDTO, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, attributeDTO(a))
	}
	slices.SortStableFunc(out, func(a, b adminAttributeDTO) int { return a.SortOrder - b.SortOrder })
	httpserver.JSON(w, http.StatusOK, out)
}

func (h *Handler) adminCreateAttribute(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code      string            `json:"code"`
		Names     map[string]string `json:"names"`
		SortOrder int               `json:"sort_order"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	a, err := h.svc.CreateAttribute(r.Context(), in.Code, textFrom(in.Names), in.SortOrder)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, attributeDTO(a))
}

func (h *Handler) adminAddAttributeValue(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code   string            `json:"code"`
		Labels map[string]string `json:"labels"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	a, err := h.svc.AddAttributeValue(r.Context(), r.PathValue("code"), in.Code, textFrom(in.Labels))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, attributeDTO(a))
}
