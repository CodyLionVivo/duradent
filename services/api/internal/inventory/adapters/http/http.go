// Package http exposes availability to the store and stock management to admins.
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/inventory/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/inventory/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
)

type Handler struct {
	svc *application.Service
	log *slog.Logger
}

func Register(mux *http.ServeMux, svc *application.Service, log *slog.Logger, admin func(http.Handler) http.Handler) {
	h := &Handler{svc: svc, log: log}
	mux.HandleFunc("GET /v1/inventory/availability", h.availability)
	a := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, admin(fn)) }
	a("GET /v1/admin/inventory", h.adminStock)
	a("POST /v1/admin/inventory/{variant}/adjust", h.adminAdjust)
	a("PUT /v1/admin/inventory/{variant}/reorder-point", h.adminReorder)
	a("GET /v1/admin/inventory/{variant}/movements", h.adminMovements)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpserver.Error(w, r, h.log, err)
}

type availabilityDTO struct {
	VariantID id.ID `json:"variant_id"`
	Available int   `json:"available"`
}

func (h *Handler) availability(w http.ResponseWriter, r *http.Request) {
	ids, err := httpserver.QueryIDs(r, "variant_id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if len(ids) > 200 {
		ids = ids[:200]
	}
	avail, err := h.svc.Availability(r.Context(), ids)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]availabilityDTO, 0, len(ids))
	for _, v := range ids {
		out = append(out, availabilityDTO{VariantID: v, Available: avail[v]})
	}
	httpserver.JSON(w, http.StatusOK, out)
}

type stockDTO struct {
	VariantID    id.ID `json:"variant_id"`
	OnHand       int   `json:"on_hand"`
	Reserved     int   `json:"reserved"`
	Available    int   `json:"available"`
	ReorderPoint int   `json:"reorder_point"`
	Low          bool  `json:"low"`
}

func toStock(v id.ID, s domain.StockItem) stockDTO {
	return stockDTO{VariantID: v, OnHand: s.OnHand, Reserved: s.Reserved, Available: s.Available(), ReorderPoint: s.ReorderPoint, Low: s.Low()}
}

func (h *Handler) adminStock(w http.ResponseWriter, r *http.Request) {
	ids, err := httpserver.QueryIDs(r, "variant_id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items, err := h.svc.Stock(r.Context(), ids)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]stockDTO, 0, len(ids))
	for _, v := range ids {
		out = append(out, toStock(v, items[v]))
	}
	httpserver.JSON(w, http.StatusOK, out)
}

func (h *Handler) adminAdjust(w http.ResponseWriter, r *http.Request) {
	vid, err := httpserver.PathID(r, "variant")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		Delta  int    `json:"delta"`
		Reason string `json:"reason"`
		Note   string `json:"note"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	reason, err := domain.ParseReason(in.Reason)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	item, err := h.svc.Adjust(r.Context(), vid, in.Delta, reason, in.Note)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toStock(vid, item))
}

func (h *Handler) adminReorder(w http.ResponseWriter, r *http.Request) {
	vid, err := httpserver.PathID(r, "variant")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		Value int `json:"value"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	item, err := h.svc.SetReorderPoint(r.Context(), vid, in.Value)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toStock(vid, item))
}

func (h *Handler) adminMovements(w http.ResponseWriter, r *http.Request) {
	vid, err := httpserver.PathID(r, "variant")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ms, err := h.svc.Movements(r.Context(), vid)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	type mDTO struct {
		Delta     int       `json:"delta"`
		Reason    string    `json:"reason"`
		Reference string    `json:"reference"`
		CreatedAt time.Time `json:"created_at"`
	}
	out := make([]mDTO, 0, len(ms))
	for _, m := range ms {
		out = append(out, mDTO{Delta: m.Delta, Reason: string(m.Reason), Reference: m.Reference, CreatedAt: m.CreatedAt})
	}
	httpserver.JSON(w, http.StatusOK, out)
}
