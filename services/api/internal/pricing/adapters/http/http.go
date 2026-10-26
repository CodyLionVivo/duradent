// Package http exposes price quotes for the store and price list management
// for the admin panel.
package http

import (
	"log/slog"
	"net/http"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/auth"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
	"github.com/CodyLionVivo/duradent/services/api/internal/pricing/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/pricing/domain"
)

type Handler struct {
	svc *application.Service
	log *slog.Logger
}

func Register(mux *http.ServeMux, svc *application.Service, log *slog.Logger, admin func(http.Handler) http.Handler) {
	h := &Handler{svc: svc, log: log}
	mux.HandleFunc("GET /v1/pricing/quotes", h.quotes)

	a := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, admin(fn)) }
	a("GET /v1/admin/pricing/price-lists", h.adminLists)
	a("POST /v1/admin/pricing/price-lists", h.adminCreateList)
	a("PUT /v1/admin/pricing/price-lists/{id}", h.adminUpdateList)
	a("GET /v1/admin/pricing/price-lists/{id}/prices", h.adminPrices)
	a("PUT /v1/admin/pricing/price-lists/{id}/variants/{variant}", h.adminSetVariantPrices)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpserver.Error(w, r, h.log, err)
}

type tierDTO struct {
	MinQty    int    `json:"min_qty"`
	UnitPrice string `json:"unit_price"`
}

type quoteDTO struct {
	VariantID id.ID     `json:"variant_id"`
	UnitPrice string    `json:"unit_price"`
	Tiers     []tierDTO `json:"tiers"`
}

// quotes prices variants for the caller: the signed-in customer's list, or
// the default (public) list for anonymous visitors. Customer lists are
// confidential, so the list never comes from a request parameter.
func (h *Handler) quotes(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	currency, err := money.ParseCurrency(valueOr(q.Get("currency"), string(money.PEN)))
	if err != nil {
		h.fail(w, r, apperr.Invalid("pricing.currency_invalid", "currency", "prices are in soles (PEN)"))
		return
	}
	qty, err := httpserver.QueryInt(r, "qty", 1, 100_000)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ids, err := httpserver.QueryIDs(r, "variant_id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var listCode string
	if p, ok := auth.FromContext(r.Context()); ok {
		listCode = p.PriceListCode
	}
	quotes, err := h.svc.Quote(r.Context(), application.QuoteQuery{ListCode: listCode, Currency: currency, VariantIDs: ids, Qty: qty})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]quoteDTO, 0, len(quotes))
	for _, vid := range ids {
		qt, ok := quotes[vid]
		if !ok {
			continue
		}
		dto := quoteDTO{VariantID: vid, UnitPrice: qt.Unit.Decimal(), Tiers: []tierDTO{}}
		for _, t := range qt.Tiers {
			dto.Tiers = append(dto.Tiers, tierDTO{MinQty: t.MinQty, UnitPrice: t.Unit.Decimal()})
		}
		items = append(items, dto)
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"currency": currency, "qty": qty, "items": items})
}

func valueOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

type listDTO struct {
	ID        id.ID  `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

func toListDTO(pl *domain.PriceList) listDTO {
	return listDTO{ID: pl.ID, Code: pl.Code, Name: pl.Name, IsDefault: pl.IsDefault}
}

func (h *Handler) adminLists(w http.ResponseWriter, r *http.Request) {
	lists, err := h.svc.PriceLists(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]listDTO, 0, len(lists))
	for _, pl := range lists {
		out = append(out, toListDTO(pl))
	}
	httpserver.JSON(w, http.StatusOK, out)
}

type listInput struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
}

func (h *Handler) adminCreateList(w http.ResponseWriter, r *http.Request) {
	var in listInput
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	pl, err := h.svc.CreatePriceList(r.Context(), in.Code, in.Name, in.IsDefault)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, toListDTO(pl))
}

func (h *Handler) adminUpdateList(w http.ResponseWriter, r *http.Request) {
	lid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in listInput
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	pl, err := h.svc.UpdatePriceList(r.Context(), lid, in.Code, in.Name, in.IsDefault)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toListDTO(pl))
}

type priceDTO struct {
	Currency  money.Currency `json:"currency"`
	MinQty    int            `json:"min_qty"`
	UnitPrice string         `json:"unit_price"`
}

type variantPricesDTO struct {
	VariantID id.ID      `json:"variant_id"`
	Prices    []priceDTO `json:"prices"`
}

func toVariantPricesDTO(vp domain.VariantPrices, vid id.ID) variantPricesDTO {
	out := variantPricesDTO{VariantID: vid, Prices: []priceDTO{}}
	for _, t := range vp.Tiers {
		out.Prices = append(out.Prices, priceDTO{Currency: t.Unit.Currency, MinQty: t.MinQty, UnitPrice: t.Unit.Decimal()})
	}
	return out
}

func (h *Handler) adminPrices(w http.ResponseWriter, r *http.Request) {
	lid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ids, err := httpserver.QueryIDs(r, "variant_id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	found, err := h.svc.PricesInList(r.Context(), lid, ids)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]variantPricesDTO, 0, len(ids))
	for _, vid := range ids {
		out = append(out, toVariantPricesDTO(found[vid], vid))
	}
	httpserver.JSON(w, http.StatusOK, out)
}

func (h *Handler) adminSetVariantPrices(w http.ResponseWriter, r *http.Request) {
	lid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	vid, err := httpserver.PathID(r, "variant")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		Prices []priceDTO `json:"prices"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	tiers := make([]domain.Tier, 0, len(in.Prices))
	for _, p := range in.Prices {
		c, err := money.ParseCurrency(string(p.Currency))
		if err != nil {
			h.fail(w, r, apperr.Invalid("pricing.currency_invalid", "prices", "prices are in soles (PEN)"))
			return
		}
		unit, err := money.Parse(p.UnitPrice, c)
		if err != nil {
			h.fail(w, r, apperr.Invalid("pricing.amount_invalid", "prices", "invalid amount "+p.UnitPrice))
			return
		}
		tiers = append(tiers, domain.Tier{MinQty: p.MinQty, Unit: unit})
	}
	vp, err := h.svc.SetVariantPrices(r.Context(), lid, vid, tiers)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toVariantPricesDTO(vp, vid))
}
