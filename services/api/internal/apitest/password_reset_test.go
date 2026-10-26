package apitest

import (
	"net/http"
	"strings"
	"testing"
)

func TestPasswordResetEndToEnd(t *testing.T) {
	s := newServer(t)
	var reg struct {
		Token string `json:"token"`
	}
	if st := s.do(http.MethodPost, "/v1/auth/register", false, map[string]string{
		"email": "olvido@clinica.pe", "password": "clave original 123", "name": "Olvido", "locale": "es",
	}, &reg); st != http.StatusCreated {
		t.Fatalf("register: %d", st)
	}

	// The answer is the same for an unknown email; only the real one gets a link.
	if st := s.do(http.MethodPost, "/v1/auth/password/forgot", false, map[string]string{"email": "nadie@clinica.pe"}, nil); st != http.StatusAccepted {
		t.Fatalf("unknown email: %d", st)
	}
	if st := s.do(http.MethodPost, "/v1/auth/password/forgot", false, map[string]string{"email": "Olvido@Clinica.pe", "locale": "pt-br"}, nil); st != http.StatusAccepted {
		t.Fatalf("forgot: %d", st)
	}
	var token string
	for _, m := range s.mail.all() {
		if m.To == "nadie@clinica.pe" {
			t.Fatal("no email for addresses without an account")
		}
		if m.To == "olvido@clinica.pe" && m.Subject == "Crie uma nova senha" {
			_, token, _ = strings.Cut(m.Text, "token=")
			token = strings.TrimSpace(token)
			if !strings.Contains(m.Text, webBaseURL+"/pt-br/account/reset?token=") {
				t.Fatalf("link: %s", m.Text)
			}
		}
	}
	if token == "" {
		t.Fatalf("no reset email: %v", subjects(s))
	}

	var p struct {
		Code string `json:"code"`
	}
	if st := s.do(http.MethodPost, "/v1/auth/password/reset", false, map[string]string{"token": token, "password": "corta"}, &p); st != http.StatusUnprocessableEntity || p.Code != "identity.password_short" {
		t.Fatalf("weak password: %d %+v", st, p)
	}
	var signed struct {
		Token   string `json:"token"`
		Account struct {
			EmailVerified bool `json:"email_verified"`
		} `json:"account"`
	}
	if st := s.do(http.MethodPost, "/v1/auth/password/reset", false, map[string]string{"token": token, "password": "una frase nueva y larga"}, &signed); st != http.StatusOK || signed.Token == "" || !signed.Account.EmailVerified {
		t.Fatalf("reset: %d %+v", st, signed)
	}
	if st := s.as(reg.Token).do(http.MethodGet, "/v1/me", false, nil, nil); st != http.StatusUnauthorized {
		t.Fatalf("the session opened before the reset must be closed: %d", st)
	}
	if st := s.as(signed.Token).do(http.MethodGet, "/v1/me", false, nil, nil); st != http.StatusOK {
		t.Fatalf("the new session works: %d", st)
	}
	if st := s.do(http.MethodPost, "/v1/auth/login", false, map[string]string{"email": "olvido@clinica.pe", "password": "una frase nueva y larga"}, nil); st != http.StatusOK {
		t.Fatalf("login with the new password: %d", st)
	}
	if st := s.do(http.MethodPost, "/v1/auth/password/reset", false, map[string]string{"token": token, "password": "otra frase cualquiera"}, &p); st != http.StatusUnprocessableEntity || p.Code != "identity.reset_invalid" {
		t.Fatalf("a link works once: %d %+v", st, p)
	}
}
