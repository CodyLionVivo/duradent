// Package http exposes electronic documents to customers (their own
// orders) and to the admin panel.
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/auth"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
)

type Handler struct {
	svc *application.Service
	log *slog.Logger
}

func Register(mux *http.ServeMux, svc *application.Service, log *slog.Logger, admin func(http.Handler) http.Handler) {
	h := &Handler{svc: svc, log: log}
	mux.Handle("GET /v1/orders/{number}/invoices", auth.RequireUser(http.HandlerFunc(h.mine)))
	mux.Handle("GET /v1/admin/invoices", admin(http.HandlerFunc(h.list)))
	mux.Handle("GET /v1/admin/orders/{id}/invoices", admin(http.HandlerFunc(h.forOrder)))
	mux.Handle("POST /v1/admin/invoices/{id}/retry", admin(http.HandlerFunc(h.retry)))
}

type invoiceDTO struct {
	ID               id.ID          `json:"id"`
	OrderID          id.ID          `json:"order_id"`
	OrderNumber      string         `json:"order_number"`
	Type             domain.DocType `json:"type"`
	Name             string         `json:"name"`
	Series           string         `json:"series"`
	Number           int            `json:"number"`
	Currency         string         `json:"currency"`
	Total            string         `json:"total"`
	Status           domain.Status  `json:"status"`
	SunatDescription string         `json:"sunat_description"`
	PDFURL           string         `json:"pdf_url"`
	XMLURL           string         `json:"xml_url"`
	CDRURL           string         `json:"cdr_url"`
	LastError        string         `json:"last_error,omitempty"`
	Attempts         int            `json:"attempts,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
}

func toDTO(inv *domain.Invoice, admin bool) invoiceDTO {
	d := invoiceDTO{
		ID: inv.ID, OrderID: inv.OrderID, OrderNumber: inv.OrderNumber, Type: inv.Type, Name: inv.Name(),
		Series: inv.Series, Number: inv.Number, Currency: string(inv.Currency), Total: inv.Total.Decimal(),
		Status: inv.Status, SunatDescription: inv.SunatDescription, PDFURL: inv.PDFURL, XMLURL: inv.XMLURL, CDRURL: inv.CDRURL,
		CreatedAt: inv.CreatedAt,
	}
	if admin {
		d.LastError, d.Attempts = inv.LastError, inv.Attempts
	}
	return d
}

func list(invs []*domain.Invoice, admin bool) []invoiceDTO {
	out := make([]invoiceDTO, 0, len(invs))
	for _, inv := range invs {
		out = append(out, toDTO(inv, admin))
	}
	return out
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpserver.Error(w, r, h.log, err)
}

func (h *Handler) mine(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	invs, err := h.svc.ForCustomer(r.Context(), p.AccountID, r.PathValue("number"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": list(invs, false)})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, err := httpserver.QueryInt(r, "page", 1, 10_000)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	f := ports.Filter{Limit: 50, Offset: (page - 1) * 50}
	switch st := domain.Status(r.URL.Query().Get("status")); st {
	case "", domain.StatusPending, domain.StatusAccepted, domain.StatusRejected:
		f.Status = st
	default:
		h.fail(w, r, apperr.Invalid("invoicing.status_invalid", "status", "unknown status"))
		return
	}
	invs, total, err := h.svc.List(r.Context(), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": list(invs, true), "total": total, "page": page, "page_size": 50})
}

func (h *Handler) forOrder(w http.ResponseWriter, r *http.Request) {
	oid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	invs, err := h.svc.ForOrder(r.Context(), oid)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": list(invs, true)})
}

func (h *Handler) retry(w http.ResponseWriter, r *http.Request) {
	iid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	inv, err := h.svc.Retry(r.Context(), iid)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toDTO(inv, true))
}
