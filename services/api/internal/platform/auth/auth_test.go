package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func resolver(ctx context.Context, token string) (Principal, bool, error) {
	switch token {
	case "admin-session":
		return Principal{Role: "admin"}, true, nil
	case "customer-session":
		return Principal{Role: "customer", PriceListCode: "clinica"}, true, nil
	case "db-down":
		return Principal{}, false, errors.New("boom")
	}
	return Principal{}, false, nil
}

func call(h http.Handler, authz string) int {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

func TestRequireAdmin(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := Middleware(resolver, log, RequireAdmin("automation-token")(ok))

	cases := map[string]int{
		"":                         http.StatusUnauthorized,
		"Session admin-session":    http.StatusNoContent,
		"Session customer-session": http.StatusForbidden,
		"Session expired":          http.StatusUnauthorized,
		"Bearer automation-token":  http.StatusNoContent,
		"Bearer wrong":             http.StatusUnauthorized,
		"Session db-down":          http.StatusServiceUnavailable,
	}
	for authz, want := range cases {
		if got := call(h, authz); got != want {
			t.Errorf("%q: got %d, want %d", authz, got, want)
		}
	}

	noToken := Middleware(resolver, log, RequireAdmin("")(ok))
	if got := call(noToken, "Bearer "); got != http.StatusUnauthorized {
		t.Errorf("empty admin token must never match, got %d", got)
	}
}

func TestPrincipalReachesHandlers(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var seen Principal
	h := Middleware(resolver, log, RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = FromContext(r.Context())
	})))
	if got := call(h, ""); got != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", got)
	}
	call(h, "Session customer-session")
	if seen.PriceListCode != "clinica" || seen.Token != "customer-session" {
		t.Fatalf("principal: %+v", seen)
	}
}
