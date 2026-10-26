package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const requestIDHeader = "X-Request-Id"

var requestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Name:    "http_request_duration_seconds",
	Help:    "HTTP request latency by route pattern, method and status.",
	Buckets: prometheus.DefBuckets,
}, []string{"route", "method", "status"})

func init() {
	prometheus.MustRegister(requestDuration)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Observe adds a request id, recovers panics, logs one line per request and
// records latency. Routes are labelled by their ServeMux pattern so metric
// cardinality stays bounded.
func Observe(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := r.Header.Get(requestIDHeader)
		if reqID == "" {
			reqID = newRequestID()
		}
		w.Header().Set(requestIDHeader, reqID)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if p := recover(); p != nil {
				log.ErrorContext(r.Context(), "panic", "panic", p, "request_id", reqID)
				rec.WriteHeader(http.StatusInternalServerError)
			}
			route := r.Pattern
			if route == "" {
				route = "unmatched"
			}
			elapsed := time.Since(start)
			requestDuration.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Observe(elapsed.Seconds())
			log.InfoContext(r.Context(), "http_request",
				"request_id", reqID, "method", r.Method, "path", r.URL.Path,
				"route", route, "status", rec.status, "duration_ms", elapsed.Milliseconds())
		}()

		next.ServeHTTP(rec, r)
	})
}

func newRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
