// Command duradent is the single backend binary. The first argument selects
// the process mode, so the same image runs as api, worker or outbox-relay on
// ECS, and as a one-off migration task before each rollout.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"

	catalogs3 "github.com/CodyLionVivo/duradent/services/api/internal/catalog/adapters/s3"
	identityoidc "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/oidc"
	identitydomain "github.com/CodyLionVivo/duradent/services/api/internal/identity/domain"
	identityports "github.com/CodyLionVivo/duradent/services/api/internal/identity/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/adapters/culqi"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/adapters/fakegateway"
	ordersports "github.com/CodyLionVivo/duradent/services/api/internal/orders/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/config"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/logging"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/seed"
	"github.com/CodyLionVivo/duradent/services/api/internal/wiring"
	"github.com/CodyLionVivo/duradent/services/api/migrations"
)

const usage = `usage: duradent <command>

commands:
  api            serve the HTTP API
  worker         background jobs (expire unpaid orders; event consumers in phase 5)
  outbox-relay   publish pending outbox rows to SNS
  migrate        apply database migrations and exit
  seed           load demo catalog, prices and stock (local only; skipped if data exists)`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	mode := os.Args[1]

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	log := logging.New(os.Stdout, cfg.LogLevel, "duradent-"+mode).With("env", cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, mode, cfg, log)
	stop()
	if err != nil {
		log.Error("exit", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, mode string, cfg config.Config, log *slog.Logger) error {
	switch mode {
	case "api", "worker", "outbox-relay", "migrate", "seed":
	default:
		return fmt.Errorf("unknown command %q\n%s", mode, usage)
	}

	db, err := mysql.Open(ctx, cfg.MySQLDSN)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if mode == "migrate" {
		if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
			return err
		}
		log.Info("migrations applied")
		return nil
	}
	if mode == "seed" && cfg.Env != "local" {
		return fmt.Errorf("seed only runs with APP_ENV=local (got %q)", cfg.Env)
	}

	app, err := build(ctx, cfg, db, log, mode == "api")
	if err != nil {
		return err
	}

	switch mode {
	case "seed":
		return seed.Run(ctx, log, app.Catalog, app.Pricing, app.Inventory)
	case "api":
		h := app.Handler(db, log, wiring.HTTPOptions{
			AdminToken: cfg.AdminToken, WebBaseURL: cfg.WebBaseURL, TrustProxy: cfg.TrustProxyHeaders,
			PublicKey: cfg.CulqiPublicKey, WebhookSecret: cfg.CulqiWebhookSecret, BankDetails: cfg.BankDetails,
		})
		return httpserver.Run(ctx, log, cfg.HTTPAddr, h, cfg.ShutdownTimeout)
	case "worker":
		go jobs(ctx, app, log)
	case "outbox-relay":
		// The relay arrives in phase 5; the mode already exposes health and
		// metrics so the deployment shape is final.
		log.Info("outbox relay not implemented yet")
	}
	mux := httpserver.NewMux(map[string]httpserver.Pinger{"mysql": db})
	return httpserver.Run(ctx, log, cfg.HTTPAddr, http.Handler(mux), cfg.ShutdownTimeout)
}

// jobs runs periodic maintenance in the worker process.
func jobs(ctx context.Context, app *wiring.App, log *slog.Logger) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		if n, err := app.Orders.ExpireOverdue(ctx); err != nil {
			log.Error("expire overdue orders", "error", err)
		} else if n > 0 {
			log.Info("expired unpaid orders", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// build wires the application. OIDC discovery only happens for the api.
func build(ctx context.Context, cfg config.Config, db *sql.DB, log *slog.Logger, withProviders bool) (*wiring.App, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	media := catalogs3.New(awsCfg, catalogs3.Options{Bucket: cfg.MediaBucket, PresignEndpoint: cfg.S3PresignEndpoint, BaseURL: cfg.MediaBaseURL})

	var gateway ordersports.PaymentGateway
	if cfg.CulqiSecretKey != "" {
		gateway = culqi.New(cfg.CulqiSecretKey, cfg.CulqiBaseURL)
	} else {
		log.Warn("CULQI_SECRET_KEY is empty: payments are simulated (local development only)")
		gateway = fakegateway.New()
	}

	providers := map[identitydomain.Provider]identityports.OIDCProvider{}
	if withProviders {
		providers = discoverProviders(ctx, cfg, log)
		if len(cfg.AdminEmails) == 0 {
			log.Warn("ADMIN_EMAILS is empty: nobody can open the admin panel with a session")
		}
	}
	return wiring.Build(wiring.Options{
		DB: db, Log: log, Media: media, Providers: providers, Gateway: gateway,
		AdminEmails: cfg.AdminEmails, DevLogin: cfg.DevLogin,
	})
}

// discoverProviders sets up OIDC providers. One that cannot be reached is
// disabled (and logged) instead of taking the whole API down.
func discoverProviders(ctx context.Context, cfg config.Config, log *slog.Logger) map[identitydomain.Provider]identityports.OIDCProvider {
	providers := map[identitydomain.Provider]identityports.OIDCProvider{}
	discover := func(name identitydomain.Provider, build func(context.Context) (*identityoidc.Provider, error)) {
		dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		p, err := build(dctx)
		if err != nil {
			log.Error("sign-in provider disabled", "provider", name, "error", err)
			return
		}
		providers[name] = p
	}
	if cfg.GoogleClientID != "" {
		discover(identitydomain.ProviderGoogle, func(c context.Context) (*identityoidc.Provider, error) {
			return identityoidc.NewGoogle(c, cfg.GoogleClientID, cfg.GoogleClientSecret)
		})
	}
	if cfg.MicrosoftClientID != "" {
		discover(identitydomain.ProviderMicrosoft, func(c context.Context) (*identityoidc.Provider, error) {
			return identityoidc.NewMicrosoft(c, cfg.MicrosoftTenant, cfg.MicrosoftClientID, cfg.MicrosoftClientSecret)
		})
	}
	return providers
}
