// Package auth resolves who is calling the API and guards routes by role.
//
// Signed-in users send "Authorization: Session <token>" (the web forwards the
// customer's session cookie this way). Automation may call admin routes with
// "Authorization: Bearer <ADMIN_TOKEN>". The principal is stored in the
// request context for handlers of every bounded context.
package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

type Principal struct {
	AccountID     id.ID
	Email         string
	Name          string
	Role          string
	PriceListCode string // empty: the public list
	Token         string // the session token itself, for logout
}

func (p Principal) IsAdmin() bool { return p.Role == "admin" }

type ctxKey struct{}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	return p, ok
}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// Resolver turns a session token into a principal; ok=false for unknown or
// expired sessions.
type Resolver func(ctx context.Context, token string) (p Principal, ok bool, err error)

// SessionToken extracts the token from "Authorization: Session <token>".
func SessionToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if t, ok := strings.CutPrefix(h, "Session "); ok {
		return strings.TrimSpace(t)
	}
	return ""
}

// Middleware attaches the principal (if any) to every request. Requests with
// an invalid session continue as anonymous: routes decide what they require.
func Middleware(resolve Resolver, log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inner := r
		if token := SessionToken(r); token != "" {
			p, ok, err := resolve(r.Context(), token)
			switch {
			case err != nil:
				log.ErrorContext(r.Context(), "resolve session", "error", err)
				writeProblem(w, http.StatusServiceUnavailable, "auth.unavailable")
				return
			case ok:
				p.Token = token
				inner = r.WithContext(WithPrincipal(r.Context(), p))
			}
		}
		next.ServeHTTP(w, inner)
		// WithContext copies the request; hand the matched ServeMux pattern
		// back so outer middleware (metrics, access log) can label the route.
		r.Pattern = inner.Pattern
	})
}

// RequireUser rejects anonymous requests with 401.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := FromContext(r.Context()); !ok {
			writeProblem(w, http.StatusUnauthorized, "auth.required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin lets through admins signed in with a session, and automation
// presenting the static admin token. An empty token disables the latter.
func RequireAdmin(adminToken string) func(http.Handler) http.Handler {
	want := []byte("Bearer " + adminToken)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p, ok := FromContext(r.Context()); ok {
				if p.IsAdmin() {
					next.ServeHTTP(w, r)
					return
				}
				writeProblem(w, http.StatusForbidden, "auth.forbidden")
				return
			}
			got := []byte(r.Header.Get("Authorization"))
			if adminToken != "" && subtle.ConstantTimeCompare(got, want) == 1 {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("WWW-Authenticate", `Session realm="duradent"`)
			writeProblem(w, http.StatusUnauthorized, "auth.required")
		})
	}
}

func writeProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code,
	})
}
