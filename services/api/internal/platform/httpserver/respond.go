package httpserver

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

const maxBodyBytes = 1 << 20 // 1 MiB: JSON commands only, images go straight to S3

// JSON writes body with the given status.
func JSON(w http.ResponseWriter, status int, body any) { writeJSON(w, status, body) }

// Problem is an RFC 9457 error body.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   string `json:"code,omitempty"`  // stable machine code, e.g. catalog.sku_taken
	Field  string `json:"field,omitempty"` // offending input field
}

// Error maps err to a Problem response. Domain errors keep their message and
// code; anything else is logged and reported as a generic 500.
func Error(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	p := Problem{Type: "about:blank"}
	if e, ok := apperr.As(err); ok {
		switch e.Kind {
		case apperr.KindInvalid:
			p.Status = http.StatusUnprocessableEntity
		case apperr.KindNotFound:
			p.Status = http.StatusNotFound
		case apperr.KindConflict:
			p.Status = http.StatusConflict
		default:
			p.Status = http.StatusInternalServerError
		}
		p.Detail, p.Code, p.Field = e.Msg, e.Code, e.Field
	} else {
		log.ErrorContext(r.Context(), "unhandled error", "error", err, "path", r.URL.Path)
		p.Status = http.StatusInternalServerError
	}
	p.Title = http.StatusText(p.Status)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

// Decode reads a JSON body into dst, rejecting unknown fields and oversized
// bodies. Errors are apperr.Invalid so Error renders them as 422.
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return apperr.Invalid("http.body_too_large", "", "request body is too large")
		case errors.Is(err, io.EOF):
			return apperr.Invalid("http.body_empty", "", "request body is empty")
		default:
			return apperr.Invalid("http.body_invalid", "", "invalid JSON: "+err.Error())
		}
	}
	if dec.More() {
		return apperr.Invalid("http.body_invalid", "", "body must contain a single JSON object")
	}
	return nil
}

// PathID parses a path wildcard as an id.
func PathID(r *http.Request, name string) (id.ID, error) {
	v, err := id.Parse(r.PathValue(name))
	if err != nil {
		return id.Nil, apperr.NotFound("http.not_found", "not found")
	}
	return v, nil
}

// QueryInt reads a positive integer query parameter, or def when absent.
func QueryInt(r *http.Request, name string, def, max int) (int, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > max {
		return 0, apperr.Invalid("http.query_invalid", name, fmt.Sprintf("%s must be between 1 and %d", name, max))
	}
	return n, nil
}

// QueryIDs reads a repeated id parameter (?variant_id=a&variant_id=b, or a comma list).
func QueryIDs(r *http.Request, name string) ([]id.ID, error) {
	var out []id.ID
	for _, raw := range r.URL.Query()[name] {
		for _, s := range strings.Split(raw, ",") {
			if s = strings.TrimSpace(s); s == "" {
				continue
			}
			v, err := id.Parse(s)
			if err != nil {
				return nil, apperr.Invalid("http.query_invalid", name, "invalid id "+s)
			}
			out = append(out, v)
		}
	}
	return out, nil
}

// RequireBearer protects admin routes with a static token until identity
// (phase 3) replaces it with sessions and roles. An empty token disables the
// routes entirely, which is the default outside local development.
func RequireBearer(token string, next http.Handler) http.Handler {
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if token == "" || subtle.ConstantTimeCompare(got, want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="duradent-admin"`)
			p := Problem{Type: "about:blank", Title: "Unauthorized", Status: http.StatusUnauthorized, Code: "auth.required"}
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(p)
			return
		}
		next.ServeHTTP(w, r)
	})
}
