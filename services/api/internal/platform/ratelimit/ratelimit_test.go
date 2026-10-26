package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAllowWithinWindow(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	l := New(3, time.Minute)
	l.now = func() time.Time { return now }

	for i := range 3 {
		if ok, _ := l.Allow("ip-1"); !ok {
			t.Fatalf("event %d should pass", i+1)
		}
	}
	ok, retry := l.Allow("ip-1")
	if ok || retry != time.Minute {
		t.Fatalf("4th event: ok=%v retry=%v", ok, retry)
	}
	if ok, _ := l.Allow("ip-2"); !ok {
		t.Fatal("keys are independent")
	}
	now = now.Add(time.Minute)
	if ok, _ := l.Allow("ip-1"); !ok {
		t.Fatal("new window resets the count")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.5:51234"
	r.Header.Set("X-Forwarded-For", "190.40.1.2, 10.0.0.9")
	if got := ClientIP(r, false); got != "10.0.0.5" {
		t.Fatalf("untrusted proxy headers must be ignored: %s", got)
	}
	if got := ClientIP(r, true); got != "190.40.1.2" {
		t.Fatalf("trusted: %s", got)
	}
}
