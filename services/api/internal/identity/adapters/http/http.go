// Package http exposes sign-in, the signed-in customer's profile (/v1/me)
// and customer administration over HTTP.
package http

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/identity/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/identity/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/auth"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/ratelimit"
)

type Options struct {
	// WebBaseURL is where providers redirect back: <WebBaseURL>/auth/<provider>/callback.
	// The redirect URI is never taken from the request.
	WebBaseURL string
	// TrustProxy trusts X-Forwarded-For for client IPs (API not publicly reachable).
	TrustProxy bool
	// PriceListExists validates codes assigned by admins (pricing context).
	PriceListExists func(ctx context.Context, code string) (bool, error)
	Admin           func(http.Handler) http.Handler
}

type Handler struct {
	svc  *application.Service
	log  *slog.Logger
	opts Options

	perIP    *ratelimit.Limiter // sign-in attempts per client IP
	perEmail *ratelimit.Limiter // password guesses per account
}

func Register(mux *http.ServeMux, svc *application.Service, log *slog.Logger, opts Options) {
	h := &Handler{
		svc: svc, log: log, opts: opts,
		perIP:    ratelimit.New(20, time.Minute),
		perEmail: ratelimit.New(5, time.Minute),
	}
	limited := func(fn http.HandlerFunc) http.Handler { return h.perIP.Middleware(opts.TrustProxy, fn) }

	mux.HandleFunc("GET /v1/auth/providers", h.providers)
	mux.Handle("POST /v1/auth/register", limited(h.register))
	mux.Handle("POST /v1/auth/login", limited(h.login))
	mux.Handle("POST /v1/auth/dev/login", limited(h.devLogin))
	mux.Handle("POST /v1/auth/oidc/{provider}/start", limited(h.oidcStart))
	mux.Handle("POST /v1/auth/oidc/{provider}/callback", limited(h.oidcCallback))
	mux.HandleFunc("POST /v1/auth/logout", h.logout)
	mux.Handle("POST /v1/auth/verify-email", limited(h.verifyEmail))
	mux.Handle("POST /v1/auth/password/forgot", limited(h.forgotPassword))
	mux.Handle("POST /v1/auth/password/reset", limited(h.resetPassword))

	u := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, auth.RequireUser(fn)) }
	u("GET /v1/me", h.me)
	u("PUT /v1/me", h.updateMe)
	u("PUT /v1/me/password", h.changePassword)
	mux.Handle("POST /v1/me/verify-email", auth.RequireUser(limited(h.resendVerification)))
	u("PUT /v1/me/customer", h.saveCustomer)
	u("POST /v1/me/addresses", h.addAddress)
	u("PUT /v1/me/addresses/{id}", h.updateAddress)
	u("DELETE /v1/me/addresses/{id}", h.removeAddress)
	u("POST /v1/me/addresses/{id}/default", h.defaultAddress)

	a := func(pattern string, fn http.HandlerFunc) { mux.Handle(pattern, opts.Admin(fn)) }
	a("GET /v1/admin/customers", h.adminCustomers)
	a("PUT /v1/admin/customers/{id}/price-list", h.adminPriceList)
	a("PUT /v1/admin/customers/{id}/role", h.adminRole)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	httpserver.Error(w, r, h.log, err)
}

func (h *Handler) client(r *http.Request) application.Client {
	return application.Client{IP: ratelimit.ClientIP(r, h.opts.TrustProxy), UserAgent: r.UserAgent()}
}

func principal(r *http.Request) auth.Principal {
	p, _ := auth.FromContext(r.Context())
	return p
}

// --- DTOs ------------------------------------------------------------------------------

type accountDTO struct {
	ID            id.ID         `json:"id"`
	Email         string        `json:"email"`
	EmailVerified bool          `json:"email_verified"`
	DisplayName   string        `json:"display_name"`
	Locale        locale.Locale `json:"locale"`
	Role          domain.Role   `json:"role"`
	HasPassword   bool          `json:"has_password"`
}

func toAccount(a *domain.Account) accountDTO {
	return accountDTO{ID: a.ID, Email: a.Email, EmailVerified: a.EmailVerified, DisplayName: a.DisplayName,
		Locale: a.Locale, Role: a.Role, HasPassword: a.PasswordHash != ""}
}

type sessionDTO struct {
	Token     string     `json:"token"`
	ExpiresAt time.Time  `json:"expires_at"`
	Account   accountDTO `json:"account"`
	ReturnTo  string     `json:"return_to,omitempty"`
}

type customerDTO struct {
	Type           domain.CustomerType `json:"type"`
	DocumentType   domain.DocumentType `json:"document_type"`
	DocumentNumber string              `json:"document_number"`
	BusinessName   string              `json:"business_name"`
	COPNumber      string              `json:"cop_number"`
	Phone          string              `json:"phone"`
	InvoiceType    string              `json:"invoice_type"`
	PriceListCode  string              `json:"price_list_code"`
}

func toCustomer(c *domain.Customer) *customerDTO {
	if c == nil {
		return nil
	}
	return &customerDTO{Type: c.Type, DocumentType: c.DocumentType, DocumentNumber: c.DocumentNumber,
		BusinessName: c.BusinessName, COPNumber: c.COPNumber, Phone: c.Phone, InvoiceType: c.InvoiceType(), PriceListCode: c.PriceListCode}
}

type addressDTO struct {
	ID         id.ID  `json:"id"`
	Label      string `json:"label"`
	Recipient  string `json:"recipient"`
	Line1      string `json:"line1"`
	Reference  string `json:"reference"`
	District   string `json:"district"`
	Province   string `json:"province"`
	Department string `json:"department"`
	Ubigeo     string `json:"ubigeo"`
	Phone      string `json:"phone"`
	IsDefault  bool   `json:"is_default"`
}

func toAddress(a *domain.Address) addressDTO {
	return addressDTO{ID: a.ID, Label: a.Label, Recipient: a.Recipient, Line1: a.Line1, Reference: a.Reference,
		District: a.District, Province: a.Province, Department: a.Department, Ubigeo: a.Ubigeo, Phone: a.Phone, IsDefault: a.IsDefault}
}

// --- sign-in -----------------------------------------------------------------------------

func (h *Handler) providers(w http.ResponseWriter, _ *http.Request) {
	httpserver.JSON(w, http.StatusOK, map[string]any{"providers": h.svc.Providers()})
}

func (h *Handler) respondSession(w http.ResponseWriter, r *http.Request, status int, s application.SignedIn, returnTo string, err error) {
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, status, sessionDTO{Token: s.Token, ExpiresAt: s.Expires, Account: toAccount(s.Account), ReturnTo: returnTo})
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
		Locale   string `json:"locale"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	s, err := h.svc.Register(r.Context(), application.RegisterInput{
		Email: in.Email, Password: in.Password, Name: in.Name, Locale: locale.ParseOrDefault(in.Locale),
	}, h.client(r))
	h.respondSession(w, r, http.StatusCreated, s, "", err)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if ok, retry := h.perEmail.Allow(strings.ToLower(strings.TrimSpace(in.Email))); !ok {
		ratelimit.TooMany(w, retry)
		return
	}
	s, err := h.svc.Login(r.Context(), in.Email, in.Password, h.client(r))
	h.respondSession(w, r, http.StatusOK, s, "", err)
}

func (h *Handler) devLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	s, err := h.svc.DevLogin(r.Context(), in.Email, in.Name, h.client(r))
	h.respondSession(w, r, http.StatusOK, s, "", err)
}

// safeReturnTo accepts only same-site paths (no open redirects).
func safeReturnTo(s string) string {
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") && !strings.HasPrefix(s, `/\`) {
		return s
	}
	return "/"
}

func (h *Handler) redirectURI(p domain.Provider) string {
	return strings.TrimSuffix(h.opts.WebBaseURL, "/") + "/auth/" + string(p) + "/callback"
}

func (h *Handler) oidcStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ReturnTo string `json:"return_to"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	p := domain.Provider(r.PathValue("provider"))
	url, state, err := h.svc.StartOIDC(r.Context(), p, h.redirectURI(p), safeReturnTo(in.ReturnTo))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, map[string]string{"authorization_url": url, "state": state})
}

func (h *Handler) oidcCallback(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code  string `json:"code"`
		State string `json:"state"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	s, returnTo, err := h.svc.FinishOIDC(r.Context(), domain.Provider(r.PathValue("provider")), in.Code, in.State, h.client(r))
	h.respondSession(w, r, http.StatusOK, s, returnTo, err)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Logout(r.Context(), auth.SessionToken(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- /v1/me --------------------------------------------------------------------------------

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	p, err := h.svc.Profile(r.Context(), principal(r).AccountID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	addrs := make([]addressDTO, 0, len(p.Addresses))
	for _, a := range p.Addresses {
		addrs = append(addrs, toAddress(a))
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{
		"account": toAccount(p.Account), "customer": toCustomer(p.Customer), "addresses": addrs,
	})
}

func (h *Handler) updateMe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		DisplayName string `json:"display_name"`
		Locale      string `json:"locale"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	var l locale.Locale
	if in.Locale != "" {
		var err error
		if l, err = locale.Parse(in.Locale); err != nil {
			h.fail(w, r, apperr.Invalid("identity.locale_invalid", "locale", "unsupported language"))
			return
		}
	}
	if err := h.svc.UpdateAccount(r.Context(), principal(r).AccountID, in.DisplayName, l); err != nil {
		h.fail(w, r, err)
		return
	}
	h.me(w, r)
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.ChangePassword(r.Context(), principal(r).AccountID, in.Current, in.New); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) saveCustomer(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Type           string `json:"type"`
		DocumentType   string `json:"document_type"`
		DocumentNumber string `json:"document_number"`
		BusinessName   string `json:"business_name"`
		COPNumber      string `json:"cop_number"`
		Phone          string `json:"phone"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	c, err := h.svc.SaveCustomer(r.Context(), principal(r).AccountID, domain.CustomerInput{
		Type: domain.CustomerType(in.Type), DocumentType: domain.DocumentType(in.DocumentType), DocumentNumber: in.DocumentNumber,
		BusinessName: in.BusinessName, COPNumber: in.COPNumber, Phone: in.Phone,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toCustomer(c))
}

type addressInput struct {
	Label      string `json:"label"`
	Recipient  string `json:"recipient"`
	Line1      string `json:"line1"`
	Reference  string `json:"reference"`
	District   string `json:"district"`
	Province   string `json:"province"`
	Department string `json:"department"`
	Ubigeo     string `json:"ubigeo"`
	Phone      string `json:"phone"`
}

func (in addressInput) domain() domain.AddressInput {
	return domain.AddressInput{Label: in.Label, Recipient: in.Recipient, Line1: in.Line1, Reference: in.Reference,
		District: in.District, Province: in.Province, Department: in.Department, Ubigeo: in.Ubigeo, Phone: in.Phone}
}

func (h *Handler) addAddress(w http.ResponseWriter, r *http.Request) {
	var in addressInput
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	a, err := h.svc.AddAddress(r.Context(), principal(r).AccountID, in.domain())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusCreated, toAddress(a))
}

func (h *Handler) updateAddress(w http.ResponseWriter, r *http.Request) {
	aid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in addressInput
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	a, err := h.svc.UpdateAddress(r.Context(), principal(r).AccountID, aid, in.domain())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.JSON(w, http.StatusOK, toAddress(a))
}

func (h *Handler) removeAddress(w http.ResponseWriter, r *http.Request) {
	aid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.RemoveAddress(r.Context(), principal(r).AccountID, aid); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) defaultAddress(w http.ResponseWriter, r *http.Request) {
	aid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.SetDefaultAddress(r.Context(), principal(r).AccountID, aid); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- admin ---------------------------------------------------------------------------------

type customerRowDTO struct {
	Account   accountDTO   `json:"account"`
	Customer  *customerDTO `json:"customer"`
	CreatedAt time.Time    `json:"created_at"`
}

func (h *Handler) adminCustomers(w http.ResponseWriter, r *http.Request) {
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
	rows, total, err := h.svc.Accounts(r.Context(), r.URL.Query().Get("q"), size, (page-1)*size)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	items := make([]customerRowDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, customerRowDTO{Account: toAccount(row.Account), Customer: toCustomer(row.Customer), CreatedAt: row.Account.CreatedAt})
	}
	httpserver.JSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "page": page, "page_size": size})
}

func (h *Handler) adminPriceList(w http.ResponseWriter, r *http.Request) {
	aid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if in.Code != "" && h.opts.PriceListExists != nil {
		ok, err := h.opts.PriceListExists(r.Context(), in.Code)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if !ok {
			h.fail(w, r, apperr.Invalid("identity.price_list_unknown", "code", "price list does not exist"))
			return
		}
	}
	if err := h.svc.AssignPriceList(r.Context(), aid, in.Code); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) adminRole(w http.ResponseWriter, r *http.Request) {
	aid, err := httpserver.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		Role string `json:"role"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	// Automation (static token) has no account: it may set any role.
	actor := principal(r).AccountID
	if err := h.svc.SetRole(r.Context(), actor, aid, domain.Role(in.Role)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.svc.VerifyEmail(r.Context(), in.Token); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// forgotPassword always answers 202 for a well-formed email: the response
// does not tell whether the address has an account.
func (h *Handler) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email  string `json:"email"`
		Locale string `json:"locale"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if ok, retry := h.perEmail.Allow("reset|" + strings.ToLower(strings.TrimSpace(in.Email))); !ok {
		ratelimit.TooMany(w, retry)
		return
	}
	if err := h.svc.RequestPasswordReset(r.Context(), in.Email, locale.ParseOrDefault(in.Locale)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := httpserver.Decode(w, r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	s, err := h.svc.ResetPassword(r.Context(), in.Token, in.Password, h.client(r))
	h.respondSession(w, r, http.StatusOK, s, "", err)
}

func (h *Handler) resendVerification(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.ResendVerification(r.Context(), principal(r).AccountID); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
