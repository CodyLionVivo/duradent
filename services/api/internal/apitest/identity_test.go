package apitest

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
)

type sessionResp struct {
	Token    string `json:"token"`
	ReturnTo string `json:"return_to"`
	Account  struct {
		ID            string `json:"id"`
		Email         string `json:"email"`
		Role          string `json:"role"`
		EmailVerified bool   `json:"email_verified"`
		HasPassword   bool   `json:"has_password"`
	} `json:"account"`
}

// googleSignIn runs the browser side of the OIDC flow against the fake provider.
func (s server) googleSignIn(t *testing.T, claims map[string]any) (sessionResp, int) {
	t.Helper()
	s.google.SignInAs(claims)
	var start struct {
		AuthorizationURL string `json:"authorization_url"`
		State            string `json:"state"`
	}
	if got := s.do("POST", "/v1/auth/oidc/google/start", false, map[string]string{"return_to": "/es/account"}, &start); got != 200 {
		t.Fatalf("start: %d", got)
	}
	if !strings.Contains(start.AuthorizationURL, "redirect_uri="+url.QueryEscape(webBaseURL+"/auth/google/callback")) {
		t.Fatalf("redirect_uri must come from config: %s", start.AuthorizationURL)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := noFollow.Get(start.AuthorizationURL) //nolint:noctx // test
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	back, _ := url.Parse(res.Header.Get("Location"))
	var out sessionResp
	code := s.do("POST", "/v1/auth/oidc/google/callback", false, map[string]string{
		"code": back.Query().Get("code"), "state": back.Query().Get("state"),
	}, &out)
	return out, code
}

func TestPasswordAccountProfileAndCustomerPrices(t *testing.T) {
	s := newServer(t)

	var reg sessionResp
	if got := s.do("POST", "/v1/auth/register", false, map[string]string{
		"email": "Dra.Ana@Clinica.pe", "password": "brackets de acero 2026", "name": "Dra. Ana", "locale": "es",
	}, &reg); got != 201 || reg.Token == "" || reg.Account.Email != "dra.ana@clinica.pe" || !reg.Account.HasPassword {
		t.Fatalf("register: %d %+v", got, reg)
	}
	var problem httpserver.Problem
	if got := s.do("POST", "/v1/auth/register", false, map[string]string{"email": "x@clinica.pe", "password": "corta"}, &problem); got != 422 || problem.Code != "identity.password_short" {
		t.Fatalf("weak password: %d %+v", got, problem)
	}
	if got := s.do("POST", "/v1/auth/login", false, map[string]string{"email": "dra.ana@clinica.pe", "password": "incorrecta 1234"}, &problem); got != 422 || problem.Code != "identity.credentials_invalid" {
		t.Fatalf("bad login: %d %+v", got, problem)
	}

	var login sessionResp
	if got := s.do("POST", "/v1/auth/login", false, map[string]string{"email": "dra.ana@clinica.pe", "password": "brackets de acero 2026"}, &login); got != 200 {
		t.Fatalf("login: %d", got)
	}
	ana := s.as(login.Token)

	if got := s.do("GET", "/v1/me", false, nil, nil); got != 401 {
		t.Fatalf("anonymous /v1/me: %d", got)
	}
	var me struct {
		Account  struct{ Email string }
		Customer *struct {
			InvoiceType string `json:"invoice_type"`
		}
		Addresses []struct{ ID string }
	}
	if got := ana.do("GET", "/v1/me", false, nil, &me); got != 200 || me.Account.Email != "dra.ana@clinica.pe" || me.Customer != nil {
		t.Fatalf("me: %d %+v", got, me)
	}
	if got := ana.do("PUT", "/v1/me/customer", false, map[string]string{
		"type": "empresa", "document_type": "RUC", "document_number": "20131312955", "business_name": "Clínica Ana SAC",
	}, nil); got != 200 {
		t.Fatalf("customer: %d", got)
	}
	if got := ana.do("PUT", "/v1/me/customer", false, map[string]string{
		"type": "persona", "document_type": "RUC", "document_number": "20100047219",
	}, &problem); got != 422 || problem.Code != "identity.ruc_invalid" {
		t.Fatalf("bad RUC: %d %+v", got, problem)
	}
	if got := ana.do("POST", "/v1/me/addresses", false, map[string]string{
		"recipient": "Dra. Ana", "line1": "Av. Larco 123", "district": "Miraflores", "province": "Lima", "department": "15",
	}, nil); got != 201 {
		t.Fatalf("address: %d", got)
	}
	_ = ana.do("GET", "/v1/me", false, nil, &me)
	if me.Customer == nil || me.Customer.InvoiceType != "01" || len(me.Addresses) != 1 {
		t.Fatalf("profile after edits: %+v", me)
	}

	// Prices: public list for everyone, clinic list once an admin assigns it.
	var cat struct{ ID string }
	s.must(201, "POST", "/v1/admin/catalog/products", map[string]any{"slug": "kit", "texts": map[string]any{"es": map[string]string{"name": "Kit"}}}, &cat)
	var prod struct{ Variants []struct{ ID string } }
	s.must(201, "POST", "/v1/admin/catalog/products/"+cat.ID+"/variants", map[string]any{"sku": "KIT-1"}, &prod)
	v := prod.Variants[0].ID
	var public, clinic struct{ ID string }
	s.must(201, "POST", "/v1/admin/pricing/price-lists", map[string]any{"code": "public", "name": "Público", "is_default": true}, &public)
	s.must(201, "POST", "/v1/admin/pricing/price-lists", map[string]any{"code": "clinica", "name": "Clínicas"}, &clinic)
	s.must(200, "PUT", "/v1/admin/pricing/price-lists/"+public.ID+"/variants/"+v, map[string]any{"prices": []map[string]any{{"currency": "PEN", "min_qty": 1, "unit_price": "100.00"}}}, nil)
	s.must(200, "PUT", "/v1/admin/pricing/price-lists/"+clinic.ID+"/variants/"+v, map[string]any{"prices": []map[string]any{{"currency": "PEN", "min_qty": 1, "unit_price": "80.00"}}}, nil)

	price := func(c client) string {
		var q struct {
			Items []struct {
				UnitPrice string `json:"unit_price"`
			}
		}
		_ = c.do("GET", "/v1/pricing/quotes?currency=PEN&variant_id="+v, false, nil, &q)
		return q.Items[0].UnitPrice
	}
	if price(s.client) != "100.00" || price(ana) != "100.00" {
		t.Fatal("before assignment everyone sees the public list")
	}
	if got := s.do("PUT", "/v1/admin/customers/"+reg.Account.ID+"/price-list", true, map[string]string{"code": "nope"}, &problem); got != 422 || problem.Code != "identity.price_list_unknown" {
		t.Fatalf("unknown list: %d %+v", got, problem)
	}
	if got := s.do("PUT", "/v1/admin/customers/"+reg.Account.ID+"/price-list", true, map[string]string{"code": "clinica"}, nil); got != 204 {
		t.Fatalf("assign list: %d", got)
	}
	if price(ana) != "80.00" || price(s.client) != "100.00" {
		t.Fatal("Ana sees clinic prices; anonymous visitors still see public ones")
	}

	// A customer is not an admin.
	if got := ana.do("GET", "/v1/admin/customers", false, nil, nil); got != 403 {
		t.Fatalf("customer on admin route: %d", got)
	}

	if got := ana.do("POST", "/v1/auth/logout", false, nil, nil); got != 204 {
		t.Fatalf("logout: %d", got)
	}
	if got := ana.do("GET", "/v1/me", false, nil, nil); got != 401 {
		t.Fatalf("after logout: %d", got)
	}
	if price(ana) != "100.00" {
		t.Fatal("a revoked session falls back to public prices")
	}
}

func TestGoogleSignInAdminAndAccountReclaim(t *testing.T) {
	s := newServer(t)

	// Someone registers the boss's address with a password they know.
	var squat sessionResp
	s.do("POST", "/v1/auth/register", false, map[string]string{"email": adminEmail, "password": "clave del intruso 99"}, &squat)
	if squat.Account.Role != "customer" {
		t.Fatal("an unverified admin email must not grant admin")
	}

	// The real owner signs in with Google.
	boss, code := s.googleSignIn(t, map[string]any{"sub": "g-boss", "email": adminEmail, "email_verified": true, "name": "Jefe"})
	if code != 200 || boss.Account.ID != squat.Account.ID || boss.Account.Role != "admin" || boss.Account.HasPassword || boss.ReturnTo != "/es/account" {
		t.Fatalf("google sign-in: %d %+v", code, boss)
	}
	if got := s.as(squat.Token).do("GET", "/v1/me", false, nil, nil); got != 401 {
		t.Fatal("the squatter's session must be revoked")
	}
	if got := s.do("POST", "/v1/auth/login", false, map[string]string{"email": adminEmail, "password": "clave del intruso 99"}, nil); got != 422 {
		t.Fatal("the squatter's password must be gone")
	}

	// The admin can use admin routes with the session.
	var customers struct{ Total int }
	if got := s.as(boss.Token).do("GET", "/v1/admin/customers", false, nil, &customers); got != 200 || customers.Total != 1 {
		t.Fatalf("admin by session: %d %+v", got, customers)
	}
	var problem httpserver.Problem
	if got := s.as(boss.Token).do("PUT", "/v1/admin/customers/"+boss.Account.ID+"/role", false, map[string]string{"role": "customer"}, &problem); got != 409 || problem.Code != "identity.self_demotion" {
		t.Fatalf("self demotion: %d %+v", got, problem)
	}

	// Replaying a callback fails.
	var start struct {
		AuthorizationURL string `json:"authorization_url"`
	}
	s.do("POST", "/v1/auth/oidc/google/start", false, map[string]string{"return_to": "https://evil.com"}, &start)
	if strings.Contains(start.AuthorizationURL, "evil.com") {
		t.Fatal("return_to must never leave the site")
	}
	if got := s.do("POST", "/v1/auth/oidc/google/callback", false, map[string]string{"code": "x", "state": "forged"}, &problem); got != 422 || problem.Code != "identity.login_state_invalid" {
		t.Fatalf("forged state: %d %+v", got, problem)
	}
	if got := s.do("POST", "/v1/auth/oidc/microsoft/start", false, map[string]string{}, nil); got != 404 {
		t.Fatalf("disabled provider: %d", got)
	}
}

func TestLoginIsRateLimitedPerAccount(t *testing.T) {
	s := newServer(t)
	s.do("POST", "/v1/auth/register", false, map[string]string{"email": "ana@clinica.pe", "password": "brackets de acero 2026"}, nil)
	last := 0
	for range 6 {
		last = s.do("POST", "/v1/auth/login", false, map[string]string{"email": "ana@clinica.pe", "password": "adivinando 1234"}, nil)
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("6th guess within a minute: %d", last)
	}
}
