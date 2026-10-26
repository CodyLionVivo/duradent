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

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"

	catalogs3 "github.com/CodyLionVivo/duradent/services/api/internal/catalog/adapters/s3"
	identityoidc "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/oidc"
	identitydomain "github.com/CodyLionVivo/duradent/services/api/internal/identity/domain"
	identityports "github.com/CodyLionVivo/duradent/services/api/internal/identity/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/adapters/nubefact"
	invoicingapp "github.com/CodyLionVivo/duradent/services/api/internal/invoicing/application"
	invoicingdomain "github.com/CodyLionVivo/duradent/services/api/internal/invoicing/domain"
	invoicingports "github.com/CodyLionVivo/duradent/services/api/internal/invoicing/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/notifications/adapters/ses"
	"github.com/CodyLionVivo/duradent/services/api/internal/notifications/adapters/smtp"
	notifapp "github.com/CodyLionVivo/duradent/services/api/internal/notifications/application"
	notifports "github.com/CodyLionVivo/duradent/services/api/internal/notifications/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/adapters/culqi"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/adapters/fakegateway"
	ordersports "github.com/CodyLionVivo/duradent/services/api/internal/orders/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/config"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/logging"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/messaging"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/outbox"
	"github.com/CodyLionVivo/duradent/services/api/internal/seed"
	"github.com/CodyLionVivo/duradent/services/api/internal/wiring"
	"github.com/CodyLionVivo/duradent/services/api/migrations"
)

const usage = `usage: duradent <command>

commands:
  api            serve the HTTP API
  worker         event consumers (emails, SUNAT documents) and periodic jobs
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

	if cfg.MySQLCAFile != "" {
		if err := mysql.RegisterCA(cfg.MySQLCAFile); err != nil {
			return err
		}
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

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return fmt.Errorf("aws config: %w", err)
	}
	app, err := build(ctx, cfg, awsCfg, db, log, mode == "api")
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
		if cfg.NotificationsQueue == "" || cfg.InvoicingQueue == "" {
			return fmt.Errorf("worker needs SQS_NOTIFICATIONS_URL and SQS_INVOICING_URL")
		}
		sqsc := sqs.NewFromConfig(awsCfg)
		notify := func(ctx context.Context, e outbox.Event) error {
			return app.Notify.Handle(ctx, notifports.Event{ID: e.ID, Type: e.Type, AggregateID: e.AggregateID, Payload: e.Payload})
		}
		invoice := func(ctx context.Context, e outbox.Event) error {
			return app.Invoicing.Handle(ctx, invoicingports.Event{ID: e.ID, Type: e.Type, AggregateID: e.AggregateID, Payload: e.Payload})
		}
		go messaging.NewConsumer("notifications", sqsc, cfg.NotificationsQueue, db, notify, log).Run(ctx)
		go messaging.NewConsumer("invoicing", sqsc, cfg.InvoicingQueue, db, invoice, log).Run(ctx)
		go jobs(ctx, app, log)
		log.Info("consuming events", "notifications", notifapp.Events, "invoicing", invoicingapp.Events)
	case "outbox-relay":
		if cfg.SNSTopicARN == "" {
			return fmt.Errorf("outbox-relay needs SNS_TOPIC_ARN")
		}
		go outbox.NewRelay(db, messaging.NewSNSPublisher(sns.NewFromConfig(awsCfg), cfg.SNSTopicARN), log).Run(ctx)
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
		if n, err := app.Invoicing.RetryStale(ctx); err != nil {
			log.Error("retry pending invoices", "error", err)
		} else if n > 0 {
			log.Info("issued pending invoices", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// build wires the application. OIDC discovery only happens for the api.
func build(ctx context.Context, cfg config.Config, awsCfg aws.Config, db *sql.DB, log *slog.Logger, withProviders bool) (*wiring.App, error) {
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
	var mailer notifports.Mailer
	if cfg.SMTPAddr != "" {
		mailer = smtp.New(cfg.SMTPAddr, cfg.MailFrom)
	} else {
		mailer = ses.New(awsCfg, cfg.MailFrom, cfg.SESConfigSet)
	}

	var ose invoicingports.Provider
	if cfg.NubefactURL != "" {
		ose = nubefact.New(cfg.NubefactURL, cfg.NubefactToken)
	} else {
		log.Warn("NUBEFACT_URL is empty: SUNAT documents are simulated (local development only)")
		ose = nubefact.NewFake()
	}

	return wiring.Build(wiring.Options{
		DB: db, Log: log, Media: media, Providers: providers, Gateway: gateway, Mailer: mailer, OSE: ose,
		AdminEmails: cfg.AdminEmails, DevLogin: cfg.DevLogin,
		WebBaseURL: cfg.WebBaseURL, BankDetails: cfg.BankDetails, OpsEmails: cfg.OpsEmails,
		InvoiceSeries: invoicingdomain.Series{
			Factura: cfg.SeriesFactura, Boleta: cfg.SeriesBoleta, CreditNoteF: cfg.SeriesCreditF, CreditNoteB: cfg.SeriesCreditB,
		},
		USDExchangeRate: cfg.USDExchangeRate,
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
