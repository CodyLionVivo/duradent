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

	cataloghttp "github.com/CodyLionVivo/duradent/services/api/internal/catalog/adapters/http"
	catalogmysql "github.com/CodyLionVivo/duradent/services/api/internal/catalog/adapters/mysql"
	catalogs3 "github.com/CodyLionVivo/duradent/services/api/internal/catalog/adapters/s3"
	catalogapp "github.com/CodyLionVivo/duradent/services/api/internal/catalog/application"
	identityargon2 "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/argon2"
	identityhttp "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/http"
	identitymysql "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/mysql"
	identityoidc "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/oidc"
	identityapp "github.com/CodyLionVivo/duradent/services/api/internal/identity/application"
	identitydomain "github.com/CodyLionVivo/duradent/services/api/internal/identity/domain"
	identityports "github.com/CodyLionVivo/duradent/services/api/internal/identity/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/auth"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/config"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/logging"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	pricinghttp "github.com/CodyLionVivo/duradent/services/api/internal/pricing/adapters/http"
	pricingmysql "github.com/CodyLionVivo/duradent/services/api/internal/pricing/adapters/mysql"
	pricingapp "github.com/CodyLionVivo/duradent/services/api/internal/pricing/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/seed"
	"github.com/CodyLionVivo/duradent/services/api/migrations"
)

const usage = `usage: duradent <command>

commands:
  api            serve the HTTP API
  worker         consume domain events from SQS
  outbox-relay   publish pending outbox rows to SNS
  migrate        apply database migrations and exit
  seed           load demo catalog and prices (local only; skipped if data exists)`

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

	switch mode {
	case "migrate":
		if err := mysql.Migrate(db, migrations.FS, "."); err != nil {
			return err
		}
		log.Info("migrations applied")
		return nil
	case "seed":
		if cfg.Env != "local" {
			return fmt.Errorf("seed only runs with APP_ENV=local (got %q)", cfg.Env)
		}
		app, err := newApp(ctx, cfg, db)
		if err != nil {
			return err
		}
		return seed.Run(ctx, log, app.catalog, app.pricing)
	}

	deps := map[string]httpserver.Pinger{"mysql": db}
	mux := httpserver.NewMux(deps)
	var handler http.Handler = mux

	switch mode {
	case "api":
		app, err := newApp(ctx, cfg, db)
		if err != nil {
			return err
		}
		identity, err := newIdentity(ctx, cfg, db, log)
		if err != nil {
			return err
		}
		admin := auth.RequireAdmin(cfg.AdminToken)
		cataloghttp.Register(mux, app.catalog, log, admin)
		pricinghttp.Register(mux, app.pricing, log, admin)
		identityhttp.Register(mux, identity, log, identityhttp.Options{
			WebBaseURL: cfg.WebBaseURL, TrustProxy: cfg.TrustProxyHeaders,
			PriceListExists: app.pricing.PriceListExists, Admin: admin,
		})
		handler = auth.Middleware(sessionResolver(identity), log, mux)
	case "worker", "outbox-relay":
		// Consumers and the relay arrive in phase 5. Until then these modes
		// only expose health and metrics so the deployment shape is final.
		log.Info("no handlers registered yet", "mode", mode)
	}

	return httpserver.Run(ctx, log, cfg.HTTPAddr, handler, cfg.ShutdownTimeout)
}

// sessionResolver adapts the identity service to the platform auth middleware.
func sessionResolver(svc *identityapp.Service) auth.Resolver {
	return func(ctx context.Context, token string) (auth.Principal, bool, error) {
		p, err := svc.Authenticate(ctx, token)
		if err != nil {
			if apperr.IsKind(err, apperr.KindNotFound) {
				return auth.Principal{}, false, nil
			}
			return auth.Principal{}, false, err
		}
		return auth.Principal{
			AccountID: p.Account.ID, Email: p.Account.Email, Name: p.Account.DisplayName,
			Role: string(p.Account.Role), PriceListCode: p.PriceListCode,
		}, true, nil
	}
}

// newIdentity wires the identity context. OIDC providers are discovered at
// start-up; one that cannot be reached is disabled (and logged) instead of
// taking the whole API down.
func newIdentity(ctx context.Context, cfg config.Config, db *sql.DB, log *slog.Logger) (*identityapp.Service, error) {
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
	if len(cfg.AdminEmails) == 0 {
		log.Warn("ADMIN_EMAILS is empty: nobody can open the admin panel with a session")
	}
	stores := identitymysql.New(db)
	return identityapp.NewService(identityapp.Deps{
		Accounts: stores.Accounts, Customers: stores.Customers, Addresses: stores.Addresses,
		Sessions: stores.Sessions, States: stores.LoginStates, Hasher: identityargon2.Hasher{},
		Providers: providers, Now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
	}, identityapp.Config{AdminEmails: cfg.AdminEmails, DevLogin: cfg.DevLogin})
}

type app struct {
	catalog *catalogapp.Service
	pricing *pricingapp.Service
}

// newApp wires adapters into the application services.
func newApp(ctx context.Context, cfg config.Config, db *sql.DB) (*app, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.AWSRegion))
	if err != nil {
		return nil, fmt.Errorf("aws config: %w", err)
	}
	media := catalogs3.New(awsCfg, catalogs3.Options{
		Bucket: cfg.MediaBucket, PresignEndpoint: cfg.S3PresignEndpoint, BaseURL: cfg.MediaBaseURL,
	})
	now := func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	return &app{
		catalog: catalogapp.NewService(
			catalogmysql.NewProducts(db), catalogmysql.NewCategories(db), catalogmysql.NewAttributes(db), media, now),
		pricing: pricingapp.NewService(pricingmysql.NewPriceLists(db), pricingmysql.NewPrices(db)),
	}, nil
}
