// Package oidc implements sign-in with OpenID Connect providers (Google,
// Microsoft Entra ID) using the authorization code flow with PKCE and nonce.
package oidc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/CodyLionVivo/duradent/services/api/internal/identity/ports"
)

const (
	GoogleIssuer = "https://accounts.google.com"
	// MicrosoftConsumerTenant is the tenant of personal Microsoft accounts,
	// whose email addresses Microsoft has verified.
	MicrosoftConsumerTenant = "9188040d-6c67-4c5b-b112-36a304b66dad"
)

type kind int

const (
	kindStandard kind = iota
	kindMicrosoft
)

type Provider struct {
	oauth    oauth2.Config
	verifier *gooidc.IDTokenVerifier
	kind     kind
	tenant   string // Microsoft: configured tenant ("common" for any account)
}

var _ ports.OIDCProvider = (*Provider)(nil)

// Options configures a standard OIDC provider; Google uses it as is.
type Options struct {
	Issuer       string
	ClientID     string
	ClientSecret string
}

// New discovers the provider's endpoints and keys from its issuer URL.
func New(ctx context.Context, o Options) (*Provider, error) {
	p, err := gooidc.NewProvider(ctx, o.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery %s: %w", o.Issuer, err)
	}
	return &Provider{
		oauth: oauth2.Config{
			ClientID: o.ClientID, ClientSecret: o.ClientSecret, Endpoint: p.Endpoint(),
			Scopes: []string{gooidc.ScopeOpenID, "email", "profile"},
		},
		verifier: p.Verifier(&gooidc.Config{ClientID: o.ClientID}),
	}, nil
}

func NewGoogle(ctx context.Context, clientID, clientSecret string) (*Provider, error) {
	return New(ctx, Options{Issuer: GoogleIssuer, ClientID: clientID, ClientSecret: clientSecret})
}

// NewMicrosoft configures Entra ID. With tenant "common" any work, school or
// personal account can sign in; the discovery document then advertises a
// templated issuer, so the issuer is checked by hand against the token's tid.
func NewMicrosoft(ctx context.Context, tenant, clientID, clientSecret string) (*Provider, error) {
	if tenant == "" {
		tenant = "common"
	}
	base := "https://login.microsoftonline.com/" + tenant + "/v2.0"
	multi := tenant == "common" || tenant == "organizations" || tenant == "consumers"
	if multi {
		ctx = gooidc.InsecureIssuerURLContext(ctx, "https://login.microsoftonline.com/{tenantid}/v2.0")
	}
	p, err := gooidc.NewProvider(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery microsoft: %w", err)
	}
	return &Provider{
		oauth: oauth2.Config{
			ClientID: clientID, ClientSecret: clientSecret, Endpoint: p.Endpoint(),
			Scopes: []string{gooidc.ScopeOpenID, "email", "profile"},
		},
		verifier: p.Verifier(&gooidc.Config{ClientID: clientID, SkipIssuerCheck: multi}),
		kind:     kindMicrosoft,
		tenant:   tenant,
	}, nil
}

func (p *Provider) config(redirectURI string) *oauth2.Config {
	c := p.oauth
	c.RedirectURL = redirectURI
	return &c
}

func (p *Provider) AuthCodeURL(state, nonce, verifier, redirectURI string) string {
	return p.config(redirectURI).AuthCodeURL(state, gooidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
}

type idClaims struct {
	Email             string `json:"email"`
	EmailVerified     *bool  `json:"email_verified"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"` // Microsoft
	TenantID          string `json:"tid"`                // Microsoft
	EmailDomainOwner  *bool  `json:"xms_edov"`           // Microsoft optional claim
}

func (p *Provider) Exchange(ctx context.Context, code, verifier, redirectURI, nonce string) (ports.Claims, error) {
	tok, err := p.config(redirectURI).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return ports.Claims{}, fmt.Errorf("token exchange: %w", err)
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return ports.Claims{}, errors.New("no id_token in token response")
	}
	idt, err := p.verifier.Verify(ctx, raw)
	if err != nil {
		return ports.Claims{}, fmt.Errorf("verify id_token: %w", err)
	}
	if idt.Nonce != nonce {
		return ports.Claims{}, errors.New("id_token nonce mismatch")
	}
	var c idClaims
	if err := idt.Claims(&c); err != nil {
		return ports.Claims{}, fmt.Errorf("id_token claims: %w", err)
	}

	out := ports.Claims{Subject: idt.Subject, Email: c.Email, Name: c.Name}
	switch p.kind {
	case kindMicrosoft:
		if c.TenantID == "" || idt.Issuer != "https://login.microsoftonline.com/"+c.TenantID+"/v2.0" {
			return ports.Claims{}, fmt.Errorf("unexpected issuer %q for tenant %q", idt.Issuer, c.TenantID)
		}
		if p.tenant == "consumers" && c.TenantID != MicrosoftConsumerTenant {
			return ports.Claims{}, errors.New("only personal Microsoft accounts are allowed")
		}
		if out.Email == "" && strings.Contains(c.PreferredUsername, "@") {
			out.Email = c.PreferredUsername
		}
		// Microsoft does not assert email_verified. Personal accounts are
		// verified by Microsoft; work accounts only if the tenant owns the
		// domain (xms_edov). Anything else is treated as unverified.
		out.EmailVerified = c.TenantID == MicrosoftConsumerTenant || (c.EmailDomainOwner != nil && *c.EmailDomainOwner)
	default:
		out.EmailVerified = c.EmailVerified != nil && *c.EmailVerified
	}
	return out, nil
}
