package config

import (
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaultsForLocal(t *testing.T) {
	cfg, err := load(env(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Env != "local" || cfg.HTTPAddr != ":8080" || cfg.ShutdownTimeout != 15*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.MySQLDSN == "" {
		t.Fatal("expected a local default DSN")
	}
}

func TestLoadRequiresDSNOutsideLocal(t *testing.T) {
	if _, err := load(env(map[string]string{"APP_ENV": "prod"})); err == nil {
		t.Fatal("expected error when MYSQL_DSN is missing in prod")
	}
}

func TestLoadParsesShutdownTimeout(t *testing.T) {
	cases := map[string]time.Duration{"30s": 30 * time.Second, "5": 5 * time.Second}
	for in, want := range cases {
		cfg, err := load(env(map[string]string{"SHUTDOWN_TIMEOUT": in}))
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		if cfg.ShutdownTimeout != want {
			t.Fatalf("%q: got %v, want %v", in, cfg.ShutdownTimeout, want)
		}
	}
	if _, err := load(env(map[string]string{"SHUTDOWN_TIMEOUT": "soon"})); err == nil {
		t.Fatal("expected error for invalid duration")
	}
}
