package id

import (
	"bytes"
	"testing"
)

func TestNewIsVersion7AndOrdered(t *testing.T) {
	prev := New()
	for range 10000 {
		next := New()
		if next[6]>>4 != 7 {
			t.Fatalf("version nibble = %x, want 7", next[6]>>4)
		}
		if next[8]>>6 != 0b10 {
			t.Fatalf("variant bits = %b, want 10", next[8]>>6)
		}
		if bytes.Compare(prev[:], next[:]) >= 0 {
			t.Fatalf("ids not strictly increasing: %s then %s", prev, next)
		}
		prev = next
	}
}

func TestStringParseRoundTrip(t *testing.T) {
	u := New()
	got, err := Parse(u.String())
	if err != nil || got != u {
		t.Fatalf("round trip: got %s, %v; want %s", got, err, u)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	for _, s := range []string{"", "not-an-id", "0190f0a1b2c37d8e9f00112233445566", "0190f0a1-b2c3-7d8e-9f00-11223344556z"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) should fail", s)
		}
	}
}

func TestScanValue(t *testing.T) {
	u := New()
	v, _ := u.Value()
	var back ID
	if err := back.Scan(v); err != nil || back != u {
		t.Fatalf("scan: %v, %s != %s", err, back, u)
	}
	if err := back.Scan("text"); err == nil {
		t.Fatal("expected error scanning a string")
	}
}
