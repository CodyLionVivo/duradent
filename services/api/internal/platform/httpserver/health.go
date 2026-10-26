package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Pinger is satisfied by *sql.DB and by any dependency readiness must check.
type Pinger interface {
	PingContext(ctx context.Context) error
}

// Liveness reports that the process is up. It never touches dependencies, so
// the ALB/ECS does not kill healthy tasks during a database incident.
func Liveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Readiness reports whether every dependency answers within a short timeout.
func Readiness(deps map[string]Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		status := http.StatusOK
		checks := make(map[string]string, len(deps))
		for name, dep := range deps {
			if err := dep.PingContext(ctx); err != nil {
				checks[name] = "down"
				status = http.StatusServiceUnavailable
				continue
			}
			checks[name] = "up"
		}
		writeJSON(w, status, map[string]any{"status": http.StatusText(status), "checks": checks})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
