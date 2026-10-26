// Package http exposes the Libro de Reclamaciones: anyone can file a sheet
// (signed in or not) and the admin panel lists and answers them.
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/complaints/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/complaints/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/complaints/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/auth"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/ratelimit"
)

type Options struct {
	Admin      func(http.Handler) http.Handler
	TrustProxy bool
}

type Handler struct {
	svc *application.Service
	log *slog.Logger
}

func Register(mux *http.ServeMux, svc *application.Service, log *slog.Logger, opts Options) {
	h := &Handler{svc: svc, log: log}
	// Filing is public: limit it per IP so the book cannot be flooded.
	perIP := ratelimit.New(10, time.Hour)
	mux.HandleFunc("GET /v1/complaints/provider", h.provider)
	mux.Handle("POST /v1/complaints", perIP.Middleware(opts.TrustProxy, http.HandlerFunc(h.file)))
	mux.Handle("GET /v1/admin/complaints", opts.Admin(http.HandlerFunc(h.list)))
	mux.Handle("GET /v1/admin/complaints/{id}", opts.Admin(http.HandlerFunc(h.get)))
	mux.Handle("POST /v1/admin/complaints/{id}/answer", opts.Admin(http.HandlerFunc(h.answer)))
}

type providerDTO struct {
	Name    string `json:"name"`
	RUC     string `json:"ruc"`
	Address string `json:"address"`
}

type consumerDTO struct {
	Name           string `json:"name"`
	DocumentType   string `json:"document_type"`
	DocumentNumber string `json:"document_number"`
	Address        string `json:"address"`
	Phone          string `json:"phone"`
	Email          string `json:"email"`
	Guardian       string `json:"guardian"`
}

type complaintDTO struct {
	ID              id.ID           `json:"id"`
	Code            string          `json:"code"`
	Kind            domain.Kind     `json:"kind"`
	Status          domain.Status   `json:"status"`
	Locale          locale.Locale   `json:"locale"`
	FiledAt         time.Time       `json:"filed_at"`
	DueAt           time.Time       `json:"due_at"`
	Provider        providerDTO     `json:"provider"`
	Consumer        consumerDTO     `json:"consumer"`
	ItemType        domain.ItemType `json:"item_type"`
	OrderNumber     string          `json:"order_number"`
	Amount          *string         `json:"amount"`
	ItemDescription string          `json:"item_description"`
	Detail          string          `json:"detail"`
	Request         string          `json:"request"`
	ResponseChannel domain.Channel  `json:"response_channel"`
	Response        string          `json:"response"`
	AnsweredAt      *time.Time      `json:"answered_at"`
	AnsweredBy      string          `json:"answered_by,omitempty"` // admin only
}

func toDTO(c *domain.Complaint, admin bool) complaintDTO {
	d := complaintDTO{
		ID: c.ID, Code: c.Code(), Kind: c.Kind, Status: c.Status, Locale: c.Locale, FiledAt: c.FiledAt, DueAt: c.DueAt,
		Provider: providerDTO{Name: c.Provider.Name, RUC: c.Provider.RUC, Address: c.Provider.Address},
		Consumer: consumerDTO{
			Name: c.Consumer.Name, DocumentType: c.Consumer.DocType, DocumentNumber: c.Consumer.DocNumber,
			Address: c.Consumer.Address, Phone: c.Consumer.Phone, Email: c.Consumer.Email, Guardian: c.Consumer.Guardian,
		},
		ItemType: c.ItemType, OrderNumber: c.OrderNumber, ItemDescription: c.ItemDescription, Detail: c.Detail,
		Request: c.Request, ResponseChannel: c.Channel, Response: c.Response,
	}
	if c.Amount != nil {
		a := c.Amount.Decimal()
		d.Amount = &a
	}
	if c.Status == domain.StatusAnswered {
		at := c.AnsweredAt
		d.AnsweredAt = &at
		if admin {
			d.AnsweredBy = c.AnsweredBy
		}
	}
	return d
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpserver.Error(w, r, h.log, err)
}

func (h *Handler) provider(w http.ResponseWriter, _ *http.Request) {
	p := h.svc.Provider()
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"provider": providerDTO{Name: p.Name, RUC: p.RUC, Address: p.Address}, "response_days": domain.ResponseDays,
	})
}

func (h *Handler) file(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind            string `json:"kind"`
		Name            string `json:"name"`
		DocumentType    string `json:"document_type"`
		DocumentNumber  string `json:"document_number"`
		Address         string `json:"address"`
		Phone           string `json:"phone"`
		Email           string `json:"email"`
		Guardian        string `json:"guardian"`
		ItemType        string `json:"item_type"`
		OrderNumber     string `json:"order_number"`
		Amount          string `json:"amount"`
		ItemDescription string `json:"item_description"`
		Detail          string `json:"detail"`
		Request         string `json:"request"`
		ResponseChannel string `json:"response_channel"`
		Locale          string `json:"locale"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	var accountID id.ID
	if p, ok := auth.FromContext(r.Context()); ok {
		accountID = p.AccountID
	}
	c, err := h.svc.File(r.Context(), domain.Input{
		Kind: in.Kind, Name: in.Name, DocType: in.DocumentType, DocNumber: in.DocumentNumber, Address: in.Address,
		Phone: in.Phone, Email: in.Email, Guardian: in.Guardian, ItemType: in.ItemType, OrderNumber: in.OrderNumber,
		Amount: in.Amount, ItemDescription: in.ItemDescription, Detail: in.Detail, Request: in.Request,
		Channel: in.ResponseChannel, Locale: locale.ParseOrDefault(in.Locale),
	}, accountID, r.Header.Get("Idempotency-Key"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, toDTO(c, false))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	page, err := httpserver.QueryInt(r, "page", 1, 10_000)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	f := ports.Filter{Query: r.URL.Query().Get("q"), Limit: 50, Offset: (page - 1) * 50}
	switch st := domain.Status(r.URL.Query().Get("status")); st {
	case "", domain.StatusOpen, domain.StatusAnswered:
		f.Status = st
	default:
		h.fail(w, r, apperr.Invalid("complaints.status_invalid", "status", "unknown status"))
		return
	}
	p, err := h.svc.List(r.Context(), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]complaintDTO, 0, len(p.Items))
	for _, c := range p.Items {
		items = append(items, toDTO(c, true))
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"items": items, "total": p.Total, "page": page, "page_size": 50, "open": p.Open, "overdue": p.Overdue,
	})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	cid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	c, err := h.svc.Get(r.Context(), cid)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toDTO(c, true))
}

func (h *Handler) answer(w http.ResponseWriter, r *http.Request) {
	cid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		Response string `json:"response"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	by := ""
	if p, ok := auth.FromContext(r.Context()); ok {
		by = p.Email
	}
	c, err := h.svc.Answer(r.Context(), cid, in.Response, by)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toDTO(c, true))
}
