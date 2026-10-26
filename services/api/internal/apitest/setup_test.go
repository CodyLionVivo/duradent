// Package apitest drives the real HTTP API (handlers, services, MySQL) end to
// end, wired by internal/wiring exactly as cmd/duradent does. Only object
// storage, Culqi, email and Nubefact are faked, and Google is an in-process OIDC provider.
package apitest

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/catalog/ports"
	identityoidc "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/oidc"
	"github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/oidc/oidctest"
	identitydomain "github.com/CodyLionVivo/duradent/services/api/internal/identity/domain"
	identityports "github.com/CodyLionVivo/duradent/services/api/internal/identity/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/adapters/nubefact"
	invoicingdomain "github.com/CodyLionVivo/duradent/services/api/internal/invoicing/domain"
	notifdomain "github.com/CodyLionVivo/duradent/services/api/internal/notifications/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/adapters/fakegateway"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysqltest"
	"github.com/CodyLionVivo/duradent/services/api/internal/wiring"
	"github.com/CodyLionVivo/duradent/services/api/migrations"
)

const (
	token         = "test-admin-token"
	adminEmail    = "jefe@duradent.pe"
	webBaseURL    = "http://web.test"
	webhookSecret = "test-webhook-secret-0123456789"
)

// inbox keeps every email the API sends.
type inbox struct {
	mu   sync.Mutex
	msgs []notifdomain.Message
}

func (b *inbox) Send(_ context.Context, m notifdomain.Message) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.msgs = append(b.msgs, m)
	return "test-" + m.To, nil
}

func (b *inbox) all() []notifdomain.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]notifdomain.Message(nil), b.msgs...)
}

type fakeMedia struct{}

func (fakeMedia) PresignUpload(_ context.Context, key, _ string, _ time.Duration) (ports.Upload, error) {
	return ports.Upload{URL: "https://bucket.test/" + key, Method: http.MethodPut}, nil
}
func (fakeMedia) PublicURL(key string) string { return "https://cdn.test/" + key }

type server struct {
	client
	google  *oidctest.Server
	gateway *fakegateway.Gateway
	mail    *inbox
	ose     *nubefact.Fake
	app     *wiring.App
	db      *sql.DB
}

type client struct {
	t       *testing.T
	srv     *httptest.Server
	session string            // sent as "Authorization: Session <token>" when set
	headers map[string]string // extra headers (cart key, idempotency key)
}

// as returns a client that calls the API as the signed-in user.
func (c client) as(session string) client {
	c.session = session
	return c
}

// with returns a client that sends an extra header.
func (c client) with(name, value string) client {
	h := map[string]string{name: value}
	for k, v := range c.headers {
		if k != name {
			h[k] = v
		}
	}
	c.headers = h
	return c
}

func newServer(t *testing.T) server {
	t.Helper()
	db := mysqltest.Start(t)
	if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	google := oidctest.New(t)
	gp, err := identityoidc.New(context.Background(), identityoidc.Options{Issuer: google.URL, ClientID: google.ClientID, ClientSecret: google.ClientSecret})
	if err != nil {
		t.Fatal(err)
	}
	gateway := fakegateway.New()
	mail, ose := &inbox{}, nubefact.NewFake()
	app, err := wiring.Build(wiring.Options{
		DB: db, Log: log, Media: fakeMedia{}, Gateway: gateway, AdminEmails: []string{adminEmail},
		Mailer: mail, OSE: ose, WebBaseURL: webBaseURL, BankDetails: "BCP 191-0000000-0-00", OpsEmails: []string{adminEmail},
		InvoiceSeries: invoicingdomain.Series{Factura: "F001", Boleta: "B001", CreditNoteF: "FC01", CreditNoteB: "BC01"},
		Providers:     map[identitydomain.Provider]identityports.OIDCProvider{identitydomain.ProviderGoogle: gp},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := app.Handler(db, log, wiring.HTTPOptions{
		AdminToken: token, WebBaseURL: webBaseURL, WebhookSecret: webhookSecret, PublicKey: "pk_test_x", BankDetails: "BCP 191-0000000-0-00",
	})
	srv := httptest.NewServer(httpserver.Observe(log, h))
	t.Cleanup(srv.Close)
	return server{client: client{t: t, srv: srv}, google: google, gateway: gateway, mail: mail, ose: ose, app: app, db: db}
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
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
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
