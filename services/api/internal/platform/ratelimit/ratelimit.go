// Package ratelimit throttles abusable endpoints (sign-in, sign-up) per key.
//
// Counters live in each API task's memory, so with N tasks the effective
// limit is N times higher. That is acceptable against password guessing at
// this scale; a shared store (e.g. ElastiCache) can replace it later.
package ratelimit

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Limiter allows `limit` events per key in each fixed window.
type Limiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	sweep   time.Time
}

type bucket struct {
	start time.Time
	count int
}

func New(limit int, window time.Duration) *Limiter {
	return &Limiter{limit: limit, window: window, now: time.Now, buckets: map[string]*bucket{}}
}

// Allow records an event for key and reports whether it is within the limit,
// and if not, how long until the window resets.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.sweep) > l.window { // drop stale buckets so memory stays bounded
		for k, b := range l.buckets {
			if now.Sub(b.start) >= l.window {
				delete(l.buckets, k)
			}
		}
		l.sweep = now
	}
	b, ok := l.buckets[key]
	if !ok || now.Sub(b.start) >= l.window {
		b = &bucket{start: now}
		l.buckets[key] = b
	}
	b.count++
	if b.count > l.limit {
		return false, b.start.Add(l.window).Sub(now)
	}
	return true, 0
}

// ClientIP returns the caller's address. Behind the ALB and the web BFF the
// real client is the first X-Forwarded-For entry, which is only trusted when
// the API is not reachable directly (trustProxy).
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Middleware limits requests per client IP.
func (l *Limiter) Middleware(trustProxy bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, retry := l.Allow(r.Pattern + "|" + ClientIP(r, trustProxy)); !ok {
			TooMany(w, retry)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// TooMany writes a 429 problem response with Retry-After.
func TooMany(w http.ResponseWriter, retry time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "about:blank", "title": "Too Many Requests", "status": 429, "code": "http.rate_limited",
	})
}
