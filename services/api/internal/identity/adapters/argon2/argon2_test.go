package argon2

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	h := Hasher{}
	hash, err := h.Hash("brackets de acero 2026")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected encoding: %s", hash)
	}
	if ok, err := h.Verify("brackets de acero 2026", hash); err != nil || !ok {
		t.Fatalf("correct password rejected: %v", err)
	}
	if ok, _ := h.Verify("otra cosa", hash); ok {
		t.Fatal("wrong password accepted")
	}
	other, _ := h.Hash("brackets de acero 2026")
	if other == hash {
		t.Fatal("hashes must be salted")
	}
}
