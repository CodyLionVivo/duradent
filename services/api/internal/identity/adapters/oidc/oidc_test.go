package oidc_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/oidc"
	"github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/oidc/oidctest"
)

const redirect = "http://web.test/auth/google/callback"

// authorize follows the provider redirect and returns the code and state.
func authorize(t *testing.T, authURL string) (code, state string) {
	t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Get(authURL) //nolint:noctx // test helper
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("authorize status %d", res.StatusCode)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	return loc.Query().Get("code"), loc.Query().Get("state")
}

func TestStandardProviderRoundTrip(t *testing.T) {
	idp := oidctest.New(t)
	ctx := context.Background()
	p, err := oidc.New(ctx, oidc.Options{Issuer: idp.URL, ClientID: idp.ClientID, ClientSecret: idp.ClientSecret})
	if err != nil {
		t.Fatal(err)
	}
	idp.SignInAs(map[string]any{"sub": "u-1", "email": "dra.ana@gmail.com", "email_verified": true, "name": "Ana"})

	code, state := authorize(t, p.AuthCodeURL("st-1", "n-1", "verifier-verifier-verifier-verifier-verifier", redirect))
	if state != "st-1" {
		t.Fatalf("state %q", state)
	}
	claims, err := p.Exchange(ctx, code, "verifier-verifier-verifier-verifier-verifier", redirect, "n-1")
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "u-1" || claims.Email != "dra.ana@gmail.com" || !claims.EmailVerified || claims.Name != "Ana" {
		t.Fatalf("claims: %+v", claims)
	}
}

func TestExchangeRejectsBadPKCEAndNonce(t *testing.T) {
	idp := oidctest.New(t)
	ctx := context.Background()
	p, _ := oidc.New(ctx, oidc.Options{Issuer: idp.URL, ClientID: idp.ClientID, ClientSecret: idp.ClientSecret})
	idp.SignInAs(map[string]any{"sub": "u-1", "email": "a@gmail.com"})

	code, _ := authorize(t, p.AuthCodeURL("s", "nonce-a", "the-right-verifier-the-right-verifier-123", redirect))
	if _, err := p.Exchange(ctx, code, "a-different-verifier-a-different-verifier", redirect, "nonce-a"); err == nil {
		t.Fatal("wrong PKCE verifier must fail")
	}

	code, _ = authorize(t, p.AuthCodeURL("s", "nonce-a", "the-right-verifier-the-right-verifier-123", redirect))
	if _, err := p.Exchange(ctx, code, "the-right-verifier-the-right-verifier-123", redirect, "nonce-b"); err == nil {
		t.Fatal("nonce mismatch must fail")
	}
}

func TestEmailVerifiedDefaultsToFalse(t *testing.T) {
	idp := oidctest.New(t)
	ctx := context.Background()
	p, _ := oidc.New(ctx, oidc.Options{Issuer: idp.URL, ClientID: idp.ClientID, ClientSecret: idp.ClientSecret})
	idp.SignInAs(map[string]any{"sub": "u-2", "email": "a@corp.pe"}) // no email_verified claim
	code, _ := authorize(t, p.AuthCodeURL("s", "n", "verifier-verifier-verifier-verifier-verifier", redirect))
	claims, err := p.Exchange(ctx, code, "verifier-verifier-verifier-verifier-verifier", redirect, "n")
	if err != nil || claims.EmailVerified {
		t.Fatalf("missing email_verified must mean unverified: %+v %v", claims, err)
	}
}

func TestWrongIssuerIsRejected(t *testing.T) {
	idp := oidctest.New(t)
	ctx := context.Background()
	p, _ := oidc.New(ctx, oidc.Options{Issuer: idp.URL, ClientID: idp.ClientID, ClientSecret: idp.ClientSecret})
	idp.Issuer = "https://evil.example"
	idp.SignInAs(map[string]any{"sub": "u-3", "email": "a@gmail.com", "email_verified": true})
	code, _ := authorize(t, p.AuthCodeURL("s", "n", "verifier-verifier-verifier-verifier-verifier", redirect))
	if _, err := p.Exchange(ctx, code, "verifier-verifier-verifier-verifier-verifier", redirect, "n"); err == nil {
		t.Fatal("token from another issuer must be rejected")
	}
}
