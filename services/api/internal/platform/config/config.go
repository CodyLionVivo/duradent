// Package config loads process configuration from environment variables.
// Secrets arrive the same way: in AWS they are injected from Secrets Manager
// into the ECS task environment, so the code never talks to Secrets Manager.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env      string // local | staging | prod
	HTTPAddr string
	MySQLDSN string
	// MySQLCAFile is a PEM bundle to verify the database certificate (RDS);
	// the DSN selects it with tls=rds.
	MySQLCAFile     string
	LogLevel        string
	ShutdownTimeout time.Duration

	// AdminToken guards /v1/admin until identity (phase 3) adds sessions and
	// roles. Empty disables admin routes.
	AdminToken string

	AWSRegion string
	// Media bucket for product images and the public URL prefix they are
	// served from (CloudFront in AWS, LocalStack path-style locally).
	MediaBucket  string
	MediaBaseURL string
	// S3PresignEndpoint is the S3 endpoint browsers can reach; set only when
	// it differs from the one the API uses (LocalStack inside Docker).
	S3PresignEndpoint string

	// WebBaseURL is the public URL of the store; OIDC providers redirect to
	// <WebBaseURL>/auth/<provider>/callback.
	WebBaseURL string
	// OIDC client credentials. A provider is enabled when its client id is set.
	GoogleClientID        string
	GoogleClientSecret    string
	MicrosoftClientID     string
	MicrosoftClientSecret string
	MicrosoftTenant       string // "common" (default), "organizations", "consumers" or a tenant id
	// AdminEmails become admins when they sign in with a verified email.
	AdminEmails []string
	// DevLogin enables password-less sign-in by email (APP_ENV=local only).
	DevLogin bool
	// TrustProxyHeaders trusts X-Forwarded-For; only safe when the API is
	// reachable solely through the web BFF / ALB.
	TrustProxyHeaders bool

	// Culqi. Without a secret key in local development a fake gateway
	// simulates payments; outside local all three values are required.
	CulqiSecretKey     string
	CulqiPublicKey     string
	CulqiWebhookSecret string // secret path segment of the webhook URL
	CulqiBaseURL       string // override for tests
	// BankDetails are the transfer instructions shown to customers.
	BankDetails string

	// Events (phase 5): the relay publishes to the topic; the worker reads
	// one queue per consuming context.
	SNSTopicARN        string
	NotificationsQueue string
	InvoicingQueue     string

	// Email. Locally SMTP_ADDR points at Mailpit; in AWS it is empty and SES
	// is used.
	MailFrom     string
	SMTPAddr     string
	SESConfigSet string
	OpsEmails    []string // stock and invoicing alerts (defaults to ADMIN_EMAILS)

	// Nubefact (OSE). Without a URL in local development a fake accepts
	// every document.
	NubefactURL   string
	NubefactToken string
	SeriesFactura string
	SeriesBoleta  string
	SeriesCreditF string
	SeriesCreditB string
}

// Load reads configuration from the environment, applying defaults suitable
// for local development. It fails if a required value is missing outside local.
func Load() (Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Env:               valueOr(getenv("APP_ENV"), "local"),
		HTTPAddr:          valueOr(getenv("HTTP_ADDR"), ":8080"),
		MySQLDSN:          getenv("MYSQL_DSN"),
		MySQLCAFile:       getenv("MYSQL_CA_FILE"),
		LogLevel:          valueOr(getenv("LOG_LEVEL"), "info"),
		AdminToken:        getenv("ADMIN_TOKEN"),
		AWSRegion:         valueOr(getenv("AWS_REGION"), "us-east-1"),
		MediaBucket:       getenv("S3_MEDIA_BUCKET"),
		MediaBaseURL:      getenv("MEDIA_BASE_URL"),
		S3PresignEndpoint: getenv("S3_PRESIGN_ENDPOINT"),

		WebBaseURL:            valueOr(getenv("WEB_BASE_URL"), "http://localhost:4321"),
		GoogleClientID:        getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:    getenv("GOOGLE_CLIENT_SECRET"),
		MicrosoftClientID:     getenv("MICROSOFT_CLIENT_ID"),
		MicrosoftClientSecret: getenv("MICROSOFT_CLIENT_SECRET"),
		MicrosoftTenant:       valueOr(getenv("MICROSOFT_TENANT"), "common"),
		AdminEmails:           splitList(getenv("ADMIN_EMAILS")),
		DevLogin:              getenv("DEV_LOGIN") == "true",
		TrustProxyHeaders:     getenv("TRUST_PROXY_HEADERS") == "true",
		CulqiSecretKey:        getenv("CULQI_SECRET_KEY"),
		CulqiPublicKey:        getenv("CULQI_PUBLIC_KEY"),
		CulqiWebhookSecret:    getenv("CULQI_WEBHOOK_SECRET"),
		CulqiBaseURL:          getenv("CULQI_BASE_URL"),
		BankDetails:           getenv("BANK_DETAILS"),

		SNSTopicARN:        getenv("SNS_TOPIC_ARN"),
		NotificationsQueue: getenv("SQS_NOTIFICATIONS_URL"),
		InvoicingQueue:     getenv("SQS_INVOICING_URL"),
		MailFrom:           valueOr(getenv("MAIL_FROM"), "Duradent <no-reply@duradent.local>"),
		SMTPAddr:           getenv("SMTP_ADDR"),
		SESConfigSet:       getenv("SES_CONFIGURATION_SET"),
		OpsEmails:          splitList(getenv("OPS_EMAILS")),
		NubefactURL:        getenv("NUBEFACT_URL"),
		NubefactToken:      getenv("NUBEFACT_TOKEN"),
		SeriesFactura:      valueOr(getenv("INVOICE_SERIES_FACTURA"), "F001"),
		SeriesBoleta:       valueOr(getenv("INVOICE_SERIES_BOLETA"), "B001"),
		SeriesCreditF:      valueOr(getenv("INVOICE_SERIES_CREDIT_F"), "FC01"),
		SeriesCreditB:      valueOr(getenv("INVOICE_SERIES_CREDIT_B"), "BC01"),
	}
	if len(cfg.OpsEmails) == 0 {
		cfg.OpsEmails = cfg.AdminEmails
	}

	timeout, err := durationOr(getenv("SHUTDOWN_TIMEOUT"), 15*time.Second)
	if err != nil {
		return Config{}, fmt.Errorf("SHUTDOWN_TIMEOUT: %w", err)
	}
	cfg.ShutdownTimeout = timeout

	if cfg.Env == "local" {
		if cfg.MySQLDSN == "" {
			cfg.MySQLDSN = "duradent:duradent@tcp(localhost:3307)/duradent?parseTime=true&multiStatements=true"
		}
		if cfg.MediaBucket == "" {
			cfg.MediaBucket = "duradent-media-local"
		}
		if cfg.MediaBaseURL == "" {
			cfg.MediaBaseURL = "http://localhost:4566/" + cfg.MediaBucket
		}
		return cfg, nil
	}

	if cfg.DevLogin {
		return Config{}, errors.New("DEV_LOGIN is only allowed with APP_ENV=local")
	}
	if !strings.HasPrefix(cfg.WebBaseURL, "https://") {
		return Config{}, errors.New("WEB_BASE_URL must use https outside local")
	}
	var missing []string
	for name, v := range map[string]string{
		"MYSQL_DSN": cfg.MySQLDSN, "S3_MEDIA_BUCKET": cfg.MediaBucket, "MEDIA_BASE_URL": cfg.MediaBaseURL,
		"CULQI_SECRET_KEY": cfg.CulqiSecretKey, "CULQI_PUBLIC_KEY": cfg.CulqiPublicKey, "CULQI_WEBHOOK_SECRET": cfg.CulqiWebhookSecret,
		"NUBEFACT_URL": cfg.NubefactURL, "NUBEFACT_TOKEN": cfg.NubefactToken,
		"MAIL_FROM": getenv("MAIL_FROM"),
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("required outside local: %v", missing)
	}
	if len(cfg.CulqiWebhookSecret) < 24 {
		return Config{}, errors.New("CULQI_WEBHOOK_SECRET must be at least 24 characters")
	}
	if cfg.AdminToken != "" && len(cfg.AdminToken) < 32 {
		return Config{}, errors.New("ADMIN_TOKEN must be at least 32 characters")
	}
	return cfg, nil
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func valueOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func durationOr(v string, fallback time.Duration) (time.Duration, error) {
	if v == "" {
		return fallback, nil
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d, nil
	}
	secs, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", v)
	}
	return time.Duration(secs) * time.Second, nil
}
