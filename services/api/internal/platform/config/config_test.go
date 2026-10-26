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

func TestDevLoginOnlyLocal(t *testing.T) {
	cfg, err := load(env(map[string]string{"DEV_LOGIN": "true", "ADMIN_EMAILS": " a@x.pe, ,b@x.pe "}))
	if err != nil || !cfg.DevLogin || len(cfg.AdminEmails) != 2 || cfg.AdminEmails[1] != "b@x.pe" {
		t.Fatalf("local: %+v %v", cfg, err)
	}
	prod := map[string]string{"APP_ENV": "prod", "MYSQL_DSN": "x", "S3_MEDIA_BUCKET": "b", "MEDIA_BASE_URL": "https://cdn", "WEB_BASE_URL": "https://duradent.pe",
		"CULQI_SECRET_KEY": "sk_live_x", "CULQI_PUBLIC_KEY": "pk_live_x", "CULQI_WEBHOOK_SECRET": "a-long-random-webhook-secret-123",
		"NUBEFACT_URL": "https://api.nubefact.com/api/v1/x", "NUBEFACT_TOKEN": "t", "MAIL_FROM": "Duradent <ventas@duradent.pe>",
		"COMPANY_RUC": "20123456789"}
	if _, err := load(env(prod)); err != nil {
		t.Fatalf("valid prod config: %v", err)
	}
	prod["COMPANY_RUC"] = "2012345678"
	if _, err := load(env(prod)); err == nil {
		t.Fatal("the complaints book needs the company's 11-digit RUC outside local")
	}
	prod["COMPANY_RUC"] = "20123456789"
	prod["DEV_LOGIN"] = "true"
	if _, err := load(env(prod)); err == nil {
		t.Fatal("DEV_LOGIN must be rejected outside local")
	}
	delete(prod, "DEV_LOGIN")
	prod["WEB_BASE_URL"] = "http://duradent.pe"
	if _, err := load(env(prod)); err == nil {
		t.Fatal("plain http web URL must be rejected outside local")
	}
}

func TestCulqiRequiredOutsideLocal(t *testing.T) {
	base := map[string]string{"APP_ENV": "staging", "MYSQL_DSN": "x", "S3_MEDIA_BUCKET": "b", "MEDIA_BASE_URL": "https://cdn", "WEB_BASE_URL": "https://staging.duradent.pe"}
	if _, err := load(env(base)); err == nil {
		t.Fatal("staging without Culqi keys must fail")
	}
	base["CULQI_SECRET_KEY"], base["CULQI_PUBLIC_KEY"], base["CULQI_WEBHOOK_SECRET"] = "sk_test_x", "pk_test_x", "short"
	if _, err := load(env(base)); err == nil {
		t.Fatal("a short webhook secret must fail")
	}
}

func TestEventsAndInvoicingSettings(t *testing.T) {
	cfg, err := load(env(map[string]string{"ADMIN_EMAILS": "jefa@duradent.pe"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SeriesFactura != "F001" || cfg.SeriesCreditB != "BC01" || cfg.OpsEmails[0] != "jefa@duradent.pe" {
		t.Fatalf("%+v", cfg)
	}
}
