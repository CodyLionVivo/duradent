package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakePinger struct{ err error }

func (f fakePinger) PingContext(context.Context) error { return f.err }

func serve(t *testing.T, deps map[string]Pinger, path string) *httptest.ResponseRecorder {
	t.Helper()
	h := Observe(slog.New(slog.NewTextHandler(io.Discard, nil)), NewMux(deps))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestLivenessIgnoresDependencies(t *testing.T) {
	rec := serve(t, map[string]Pinger{"mysql": fakePinger{errors.New("down")}}, "/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if rec.Header().Get(requestIDHeader) == "" {
		t.Fatal("expected a request id header")
	}
}

func TestReadinessReportsEachDependency(t *testing.T) {
	rec := serve(t, map[string]Pinger{"mysql": fakePinger{}}, "/readyz")
	if rec.Code != http.StatusOK {
		t.Fatalf("healthy deps: got %d, want 200", rec.Code)
	}

	rec = serve(t, map[string]Pinger{"mysql": fakePinger{}, "sqs": fakePinger{errors.New("timeout")}}, "/readyz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("failing dep: got %d, want 503", rec.Code)
	}
	var body struct {
		Checks map[string]string `json:"checks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Checks["mysql"] != "up" || body.Checks["sqs"] != "down" {
		t.Fatalf("unexpected checks: %v", body.Checks)
	}
}

func TestObserveRecoversPanics(t *testing.T) {
	h := Observe(slog.New(slog.NewTextHandler(io.Discard, nil)), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want 500", rec.Code)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	if rec := serve(t, nil, "/metrics"); rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
}
