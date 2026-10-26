// Package apitest drives the real HTTP API (handlers, services, MySQL) end to
// end, wired as in cmd/duradent. Only object storage is faked, and Google is
// played by an in-process OIDC provider.
package apitest

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	cataloghttp "github.com/CodyLionVivo/duradent/services/api/internal/catalog/adapters/http"
	catalogmysql "github.com/CodyLionVivo/duradent/services/api/internal/catalog/adapters/mysql"
	catalogapp "github.com/CodyLionVivo/duradent/services/api/internal/catalog/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/ports"
	identityargon2 "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/argon2"
	identityhttp "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/http"
	identitymysql "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/mysql"
	identityoidc "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/oidc"
	"github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/oidc/oidctest"
	identityapp "github.com/CodyLionVivo/duradent/services/api/internal/identity/application"
	identitydomain "github.com/CodyLionVivo/duradent/services/api/internal/identity/domain"
	identityports "github.com/CodyLionVivo/duradent/services/api/internal/identity/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/auth"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysqltest"
	pricinghttp "github.com/CodyLionVivo/duradent/services/api/internal/pricing/adapters/http"
	pricingmysql "github.com/CodyLionVivo/duradent/services/api/internal/pricing/adapters/mysql"
	pricingapp "github.com/CodyLionVivo/duradent/services/api/internal/pricing/application"
	"github.com/CodyLionVivo/duradent/services/api/migrations"
)

const (
	token      = "test-admin-token"
	adminEmail = "jefe@duradent.pe"
	webBaseURL = "http://web.test"
)

type fakeMedia struct{}

func (fakeMedia) PresignUpload(_ context.Context, key, _ string, _ time.Duration) (ports.Upload, error) {
	return ports.Upload{URL: "https://bucket.test/" + key, Method: http.MethodPut}, nil
}
func (fakeMedia) PublicURL(key string) string { return "https://cdn.test/" + key }

type server struct {
	client
	google *oidctest.Server
}

type client struct {
	t       *testing.T
	srv     *httptest.Server
	session string // sent as "Authorization: Session <token>" when set
}

// as returns a client that calls the API as the signed-in user.
func (c client) as(session string) client {
	c.session = session
	return c
}

func newServer(t *testing.T) server {
	t.Helper()
	db := mysqltest.Start(t)
	if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	catalog := catalogapp.NewService(catalogmysql.NewProducts(db), catalogmysql.NewCategories(db), catalogmysql.NewAttributes(db), fakeMedia{}, now)
	pricing := pricingapp.NewService(pricingmysql.NewPriceLists(db), pricingmysql.NewPrices(db))

	google := oidctest.New(t)
	gp, err := identityoidc.New(context.Background(), identityoidc.Options{Issuer: google.URL, ClientID: google.ClientID, ClientSecret: google.ClientSecret})
	if err != nil {
		t.Fatal(err)
	}
	stores := identitymysql.New(db)
	identity, err := identityapp.NewService(identityapp.Deps{
		Accounts: stores.Accounts, Customers: stores.Customers, Addresses: stores.Addresses,
		Sessions: stores.Sessions, States: stores.LoginStates, Hasher: identityargon2.Hasher{},
		Providers: map[identitydomain.Provider]identityports.OIDCProvider{identitydomain.ProviderGoogle: gp},
		Now:       now,
	}, identityapp.Config{AdminEmails: []string{adminEmail}})
	if err != nil {
		t.Fatal(err)
	}

	mux := httpserver.NewMux(map[string]httpserver.Pinger{"mysql": db})
	admin := auth.RequireAdmin(token)
	cataloghttp.Register(mux, catalog, log, admin)
	pricinghttp.Register(mux, pricing, log, admin)
	identityhttp.Register(mux, identity, log, identityhttp.Options{
		WebBaseURL: webBaseURL, PriceListExists: pricing.PriceListExists, Admin: admin,
	})
	resolve := func(ctx context.Context, tok string) (auth.Principal, bool, error) {
		p, err := identity.Authenticate(ctx, tok)
		if apperr.IsKind(err, apperr.KindNotFound) {
			return auth.Principal{}, false, nil
		}
		if err != nil {
			return auth.Principal{}, false, err
		}
		return auth.Principal{AccountID: p.Account.ID, Email: p.Account.Email, Role: string(p.Account.Role), PriceListCode: p.PriceListCode}, true, nil
	}
	srv := httptest.NewServer(httpserver.Observe(log, auth.Middleware(resolve, log, mux)))
	t.Cleanup(srv.Close)
	return server{client: client{t: t, srv: srv}, google: google}
}

// do sends a request and decodes the JSON response into out (if not nil).
// admin adds the automation bearer token.
func (c client) do(method, path string, admin bool, body any, out any) int {
	c.t.Helper()
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, c.srv.URL+path, buf)
	req.Header.Set("Content-Type", "application/json")
	switch {
	case admin:
		req.Header.Set("Authorization", "Bearer "+token)
	case c.session != "":
		req.Header.Set("Authorization", "Session "+c.session)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decode %q: %v", method, path, raw, err)
		}
	}
	return res.StatusCode
}
