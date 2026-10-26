package mysql

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegisterCARejectsMissingOrEmptyBundles(t *testing.T) {
	if err := RegisterCA(filepath.Join(t.TempDir(), "missing.pem")); err == nil {
		t.Fatal("missing file")
	}
	empty := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(empty, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RegisterCA(empty); err == nil {
		t.Fatal("a bundle without certificates must fail")
	}
}
