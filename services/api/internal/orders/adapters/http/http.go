// Package http exposes the cart, checkout, payments (Culqi and bank transfer)
// and order administration.
package http

import (
	"crypto/subtle"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/adapters/culqi"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/auth"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/ratelimit"
)

type Options struct {
	Admin         func(http.Handler) http.Handler
	TrustProxy    bool
	PublicKey     string // Culqi public key (pk_…), safe to expose
	WebhookSecret string // secret path segment of the webhook URL
	BankDetails   string // transfer instructions shown to customers
}

type Handler struct {
	svc  *application.Service
	log  *slog.Logger
	opts Options
	pay  *ratelimit.Limiter
}

// CartKeyHeader carries an anonymous visitor's cart key (the web keeps it in a cookie).
const CartKeyHeader = "X-Cart-Key"

func Register(mux *http.ServeMux, svc *application.Service, log *slog.Logger, opts Options) {
	h := &Handler{svc: svc, log: log, opts: opts, pay: ratelimit.New(10, time.Minute)}

	mux.HandleFunc("GET /v1/cart", h.cart)
	mux.HandleFunc("POST /v1/cart/items", h.addItem)
	mux.HandleFunc("PUT /v1/cart/items/{variant}", h.setItem)
	mux.HandleFunc("DELETE /v1/cart/items/{variant}", h.removeItem)
	mux.HandleFunc("GET /v1/payments/config", h.paymentsConfig)
	mux.HandleFunc("POST /v1/webhooks/culqi/{secret}", h.culqiWebhook)

	u := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, auth.RequireUser(fn)) }
	u("POST /v1/cart/merge", h.mergeCart)
	u("POST /v1/checkout/quote", h.quote)
	u("POST /v1/orders", h.placeOrder)
	u("GET /v1/orders", h.myOrders)
	u("GET /v1/orders/{number}", h.myOrder)
	mux.Handle("POST /v1/orders/{number}/pay", auth.RequireUser(h.pay.Middleware(opts.TrustProxy, http.HandlerFunc(h.payOrder))))
	u("POST /v1/orders/{number}/cancel", h.cancelOrder)

	a := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, opts.Admin(fn)) }
	a("GET /v1/admin/orders", h.adminOrders)
	a("GET /v1/admin/orders/{id}", h.adminOrder)
	a("POST /v1/admin/orders/{id}/confirm-transfer", h.adminConfirmTransfer)
	a("POST /v1/admin/orders/{id}/prepare", h.adminPrepare)
	a("POST /v1/admin/orders/{id}/ship", h.adminShip)
	a("POST /v1/admin/orders/{id}/deliver", h.adminDeliver)
	a("POST /v1/admin/orders/{id}/cancel", h.adminCancel)
	a("POST /v1/admin/orders/{id}/refund", h.adminRefund)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpserver.Error(w, r, h.log, err)
}

func principal(r *http.Request) (auth.Principal, bool) { return auth.FromContext(r.Context()) }

func (h *Handler) owner(r *http.Request) application.CartOwner {
	if p, ok := principal(r); ok {
		return application.CartOwner{AccountID: p.AccountID}
	}
	return application.CartOwner{Key: strings.TrimSpace(r.Header.Get(CartKeyHeader))}
}

func currencyOf(r *http.Request, raw string) (money.Currency, error) {
	if raw == "" {
		raw = r.URL.Query().Get("currency")
	}
	if raw == "" {
		return money.PEN, nil
	}
	c, err := money.ParseCurrency(raw)
	if err != nil {
		return "", apperr.Invalid("orders.currency_invalid", "currency", "use PEN or USD")
	}
	return c, nil
}

// --- DTOs ------------------------------------------------------------------------------

func dec(m *money.Money) *string {
	if m == nil {
		return nil
	}
	s := m.Decimal()
	return &s
}

type cartLineDTO struct {
	VariantID   id.ID   `json:"variant_id"`
	SKU         string  `json:"sku"`
	ProductName string  `json:"product_name"`
	ProductSlug string  `json:"product_slug"`
	Options     string  `json:"options"`
	ImageURL    string  `json:"image_url"`
	Qty         int     `json:"qty"`
	UnitPrice   *string `json:"unit_price"`
	LineTotal   *string `json:"line_total"`
	Available   int     `json:"available"`
	Problem     string  `json:"problem"`
}

type cartDTO struct {
	Key       string         `json:"key,omitempty"`
	Currency  money.Currency `json:"currency"`
	Lines     []cartLineDTO  `json:"lines"`
	Subtotal  string         `json:"subtotal"`
	ItemCount int            `json:"item_count"`
	WeightG   int            `json:"weight_g"`
	Ready     bool           `json:"ready"`
}

func toCart(v application.CartView) cartDTO {
	out := cartDTO{Key: v.Key, Currency: v.Subtotal.Currency, Lines: []cartLineDTO{}, Subtotal: v.Subtotal.Decimal(),
		ItemCount: v.ItemCount, WeightG: v.WeightG, Ready: v.Ready}
	for _, l := range v.Lines {
		out.Lines = append(out.Lines, cartLineDTO{
			VariantID: l.VariantID, SKU: l.SKU, ProductName: l.ProductName, ProductSlug: l.ProductSlug, Options: l.Options,
			ImageURL: l.ImageURL, Qty: l.Qty, UnitPrice: dec(l.UnitPrice), LineTotal: dec(l.LineTotal), Available: l.Available, Problem: l.Problem,
		})
	}
	return out
}

type orderLineDTO struct {
	VariantID id.ID  `json:"variant_id"`
	SKU       string `json:"sku"`
	Name      string `json:"name"`
	Options   string `json:"options"`
	UnitPrice string `json:"unit_price"`
	Qty       int    `json:"qty"`
	LineTotal string `json:"line_total"`
}

type orderDTO struct {
	ID              id.ID                `json:"id"`
	Number          string               `json:"number"`
	Status          domain.Status        `json:"status"`
	PaymentMethod   domain.PaymentMethod `json:"payment_method"`
	Currency        money.Currency       `json:"currency"`
	Email           string               `json:"email"`
	Lines           []orderLineDTO       `json:"lines"`
	Subtotal        string               `json:"subtotal"`
	ShippingAmount  string               `json:"shipping_amount"`
	Tax             string               `json:"tax"`
	Total           string               `json:"total"`
	Billing         domain.Billing       `json:"billing"`
	Shipping        domain.Shipping      `json:"shipping"`
	Notes           string               `json:"notes"`
	PaymentDeadline time.Time            `json:"payment_deadline"`
	PaymentPending  bool                 `json:"payment_in_progress"`
	Carrier         string               `json:"carrier"`
	Tracking        string               `json:"tracking"`
	CancelReason    string               `json:"cancel_reason"`
	CreatedAt       time.Time            `json:"created_at"`
	PaidAt          *time.Time           `json:"paid_at"`
	ShippedAt       *time.Time           `json:"shipped_at"`
	DeliveredAt     *time.Time           `json:"delivered_at"`
	CancelledAt     *time.Time           `json:"cancelled_at"`
	RefundedAt      *time.Time           `json:"refunded_at"`
	RefundRef       string               `json:"refund_ref"`
}

func toOrder(o *domain.Order) orderDTO {
	out := orderDTO{
		ID: o.ID, Number: o.Number, Status: o.Status, PaymentMethod: o.Method, Currency: o.Currency, Email: o.Email,
		Lines: []orderLineDTO{}, Subtotal: o.Subtotal.Decimal(), ShippingAmount: o.ShippingAmount.Decimal(),
		Tax: o.Tax.Decimal(), Total: o.Total.Decimal(), Billing: o.Billing, Shipping: o.Shipping, Notes: o.Notes,
		PaymentDeadline: o.PaymentDeadline, PaymentPending: o.ChargingUntil != nil && time.Now().Before(*o.ChargingUntil),
		Carrier: o.Carrier, Tracking: o.Tracking, CancelReason: o.CancelReason, CreatedAt: o.CreatedAt,
		PaidAt: o.PaidAt, ShippedAt: o.ShippedAt, DeliveredAt: o.DeliveredAt, CancelledAt: o.CancelledAt,
		RefundedAt: o.RefundedAt, RefundRef: o.RefundRef,
	}
	for _, l := range o.Lines {
		out.Lines = append(out.Lines, orderLineDTO{VariantID: l.VariantID, SKU: l.SKU, Name: l.Name, Options: l.Options,
			UnitPrice: l.UnitPrice.Decimal(), Qty: l.Qty, LineTotal: l.LineTotal.Decimal()})
	}
	return out
}

type pageDTO[T any] struct {
	Items    []T `json:"items"`
	Total    int `json:"total"`
	Page     int `json:"page"`
	PageSize int `json:"page_size"`
}

// --- cart ------------------------------------------------------------------------------

func (h *Handler) cartQuery(r *http.Request) (application.CartQuery, error) {
	cur, err := currencyOf(r, "")
	if err != nil {
		return application.CartQuery{}, err
	}
	q := application.CartQuery{Owner: h.owner(r), Currency: cur, Locale: locale.ParseOrDefault(r.URL.Query().Get("locale"))}
	if p, ok := principal(r); ok {
		q.PriceListCode = p.PriceListCode
	}
	return q, nil
}

func (h *Handler) respondCart(w http.ResponseWriter, r *http.Request, keyOverride string) {
	q, err := h.cartQuery(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if keyOverride != "" {
		q.Owner.Key = keyOverride
	}
	v, err := h.svc.Cart(r.Context(), q)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toCart(v))
}

func (h *Handler) cart(w http.ResponseWriter, r *http.Request) { h.respondCart(w, r, "") }

func (h *Handler) addItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		VariantID id.ID `json:"variant_id"`
		Qty       int   `json:"qty"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	key, err := h.svc.AddToCart(r.Context(), h.owner(r), in.VariantID, in.Qty)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.respondCart(w, r, key)
}

func (h *Handler) setItem(w http.ResponseWriter, r *http.Request) {
	vid, err := httpserver.PathID(r, "variant")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		Qty int `json:"qty"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.SetCartQty(r.Context(), h.owner(r), vid, in.Qty); err != nil {
		h.fail(w, r, err)
		return
	}
	h.respondCart(w, r, "")
}

func (h *Handler) removeItem(w http.ResponseWriter, r *http.Request) {
	vid, err := httpserver.PathID(r, "variant")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.SetCartQty(r.Context(), h.owner(r), vid, 0); err != nil {
		h.fail(w, r, err)
		return
	}
	h.respondCart(w, r, "")
}

func (h *Handler) mergeCart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p, _ := principal(r)
	if err := h.svc.MergeCart(r.Context(), p.AccountID, in.Key); err != nil {
		h.fail(w, r, err)
		return
	}
	h.respondCart(w, r, "")
}

// --- checkout --------------------------------------------------------------------------

type checkoutInput struct {
	Currency      string `json:"currency"`
	AddressID     *id.ID `json:"address_id"`
	Pickup        bool   `json:"pickup"`
	PaymentMethod string `json:"payment_method"`
	Notes         string `json:"notes"`
}

func (h *Handler) checkoutIn(r *http.Request, in checkoutInput, needMethod bool) (application.CheckoutInput, error) {
	p, _ := principal(r)
	cur, err := currencyOf(r, in.Currency)
	if err != nil {
		return application.CheckoutInput{}, err
	}
	out := application.CheckoutInput{AccountID: p.AccountID, Currency: cur, Pickup: in.Pickup, Notes: in.Notes,
		IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key"))}
	if in.AddressID != nil {
		out.AddressID = *in.AddressID
	}
	if needMethod || in.PaymentMethod != "" {
		if out.Method, err = domain.ParseMethod(in.PaymentMethod); err != nil {
			return application.CheckoutInput{}, err
		}
	}
	if len(out.IdempotencyKey) > 128 {
		return application.CheckoutInput{}, apperr.Invalid("orders.idempotency_key_invalid", "Idempotency-Key", "key too long")
	}
	return out, nil
}

func (h *Handler) quote(w http.ResponseWriter, r *http.Request) {
	var in checkoutInput
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	ci, err := h.checkoutIn(r, in, false)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	q, err := h.svc.QuoteCheckout(r.Context(), ci)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"cart": toCart(q.Cart), "shipping": q.Shipping, "shipping_amount": q.ShipCost.Decimal(),
		"tax": q.Tax.Decimal(), "total": q.Total.Decimal(), "billing": q.Billing, "igv_percent": domain.IGVPercent,
	})
}

func (h *Handler) placeOrder(w http.ResponseWriter, r *http.Request) {
	var in checkoutInput
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	ci, err := h.checkoutIn(r, in, true)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	o, err := h.svc.PlaceOrder(r.Context(), ci)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, toOrder(o))
}

func (h *Handler) myOrders(w http.ResponseWriter, r *http.Request) {
	page, err := httpserver.QueryInt(r, "page", 1, 10_000)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	p, _ := principal(r)
	orders, total, err := h.svc.MyOrders(r.Context(), p.AccountID, 20, (page-1)*20)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]orderDTO, 0, len(orders))
	for _, o := range orders {
		items = append(items, toOrder(o))
	}
	httpserver.JSON(w, http.StatusOK, pageDTO[orderDTO]{Items: items, Total: total, Page: page, PageSize: 20})
}

func (h *Handler) myOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	o, err := h.svc.MyOrder(r.Context(), p.AccountID, r.PathValue("number"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toOrder(o))
}

func (h *Handler) payOrder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p, _ := principal(r)
	o, err := h.svc.PayWithCulqi(r.Context(), p.AccountID, r.PathValue("number"), in.Token, ratelimit.ClientIP(r, h.opts.TrustProxy))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toOrder(o))
}

func (h *Handler) cancelOrder(w http.ResponseWriter, r *http.Request) {
	p, _ := principal(r)
	o, err := h.svc.CancelMine(r.Context(), p.AccountID, r.PathValue("number"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toOrder(o))
}

func (h *Handler) paymentsConfig(w http.ResponseWriter, _ *http.Request) {
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"gateway": h.svc.GatewayName(), "culqi_public_key": h.opts.PublicKey, "bank_details": h.opts.BankDetails,
		"yape_max_pen": money.New(domain.YapeMaxPEN, money.PEN).Decimal(),
	})
}

// culqiWebhook accepts Culqi events. Culqi does not sign webhooks, so the URL
// carries a secret and the charge is always re-read from Culqi's API before
// anything changes (see application.Reconcile). Unknown events get 200 so
// Culqi stops retrying them.
func (h *Handler) culqiWebhook(w http.ResponseWriter, r *http.Request) {
	secret := r.PathValue("secret")
	if h.opts.WebhookSecret == "" || subtle.ConstantTimeCompare([]byte(secret), []byte(h.opts.WebhookSecret)) != 1 {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		h.fail(w, r, apperr.Invalid("http.body_invalid", "", "unreadable body"))
		return
	}
	chargeID, err := culqi.WebhookChargeID(body)
	if err != nil {
		h.log.WarnContext(r.Context(), "culqi webhook: unparseable", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	if chargeID != "" {
		if err := h.svc.Reconcile(r.Context(), chargeID); err != nil {
			h.log.ErrorContext(r.Context(), "culqi webhook: reconcile", "charge", chargeID, "error", err)
			w.WriteHeader(http.StatusInternalServerError) // Culqi retries
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

// --- admin -----------------------------------------------------------------------------

type paymentDTO struct {
	ID             id.ID                `json:"id"`
	Method         domain.PaymentMethod `json:"method"`
	Status         domain.PaymentStatus `json:"status"`
	Amount         string               `json:"amount"`
	Currency       money.Currency       `json:"currency"`
	ProviderRef    string               `json:"provider_ref"`
	SourceType     string               `json:"source_type"`
	FailureCode    string               `json:"failure_code"`
	FailureMessage string               `json:"failure_message"`
	Reference      string               `json:"reference"`
	CreatedAt      time.Time            `json:"created_at"`
}

type historyDTO struct {
	From  domain.Status `json:"from"`
	To    domain.Status `json:"to"`
	Actor string        `json:"actor"`
	Note  string        `json:"note"`
	At    time.Time     `json:"at"`
}

func (h *Handler) adminOrders(w http.ResponseWriter, r *http.Request) {
	page, err := httpserver.QueryInt(r, "page", 1, 10_000)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	f := ports.OrderFilter{Query: strings.TrimSpace(r.URL.Query().Get("q")), Limit: 50, Offset: (page - 1) * 50}
	if s := r.URL.Query().Get("status"); s != "" {
		st, err := domain.ParseStatus(s)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		f.Statuses = []domain.Status{st}
	}
	orders, total, err := h.svc.Orders(r.Context(), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]orderDTO, 0, len(orders))
	for _, o := range orders {
		items = append(items, toOrder(o))
	}
	httpserver.JSON(w, http.StatusOK, pageDTO[orderDTO]{Items: items, Total: total, Page: page, PageSize: 50})
}

func (h *Handler) adminOrder(w http.ResponseWriter, r *http.Request) {
	oid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d, err := h.svc.Detail(r.Context(), oid)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	pays := make([]paymentDTO, 0, len(d.Payments))
	for _, p := range d.Payments {
		pays = append(pays, paymentDTO{ID: p.ID, Method: p.Method, Status: p.Status, Amount: p.Amount.Decimal(), Currency: p.Amount.Currency,
			ProviderRef: p.ProviderRef, SourceType: p.SourceType, FailureCode: p.FailureCode, FailureMessage: p.FailureMessage,
			Reference: p.Reference, CreatedAt: p.CreatedAt})
	}
	hist := make([]historyDTO, 0, len(d.History))
	for _, c := range d.History {
		hist = append(hist, historyDTO{From: c.From, To: c.To, Actor: c.Actor, Note: c.Note, At: c.At})
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"order": toOrder(d.Order), "payments": pays, "history": hist})
}

func adminID(r *http.Request) id.ID {
	p, _ := principal(r)
	return p.AccountID
}

func (h *Handler) adminAction(w http.ResponseWriter, r *http.Request, run func(id.ID) (*domain.Order, error)) {
	oid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	o, err := run(oid)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toOrder(o))
}

func (h *Handler) adminConfirmTransfer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reference string `json:"reference"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	h.adminAction(w, r, func(oid id.ID) (*domain.Order, error) {
		return h.svc.ConfirmTransfer(r.Context(), adminID(r), oid, in.Reference)
	})
}

func (h *Handler) adminPrepare(w http.ResponseWriter, r *http.Request) {
	h.adminAction(w, r, func(oid id.ID) (*domain.Order, error) { return h.svc.StartPreparing(r.Context(), adminID(r), oid) })
}

func (h *Handler) adminShip(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Carrier  string `json:"carrier"`
		Tracking string `json:"tracking"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	h.adminAction(w, r, func(oid id.ID) (*domain.Order, error) {
		return h.svc.Ship(r.Context(), adminID(r), oid, in.Carrier, in.Tracking)
	})
}

func (h *Handler) adminDeliver(w http.ResponseWriter, r *http.Request) {
	h.adminAction(w, r, func(oid id.ID) (*domain.Order, error) { return h.svc.Deliver(r.Context(), adminID(r), oid) })
}

func (h *Handler) adminCancel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	h.adminAction(w, r, func(oid id.ID) (*domain.Order, error) {
		return h.svc.CancelByAdmin(r.Context(), adminID(r), oid, in.Reason)
	})
}

func (h *Handler) adminRefund(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason    string `json:"reason"`
		Reference string `json:"reference"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	h.adminAction(w, r, func(oid id.ID) (*domain.Order, error) {
		return h.svc.Refund(r.Context(), adminID(r), oid, in.Reason, in.Reference)
	})
}
