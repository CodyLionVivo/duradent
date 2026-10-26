// Package wiring is the composition root shared by cmd/duradent and the
// end-to-end tests: it builds every bounded context from its adapters and
// mounts their HTTP routes, so tests exercise exactly what production runs.
package wiring

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"time"

	cataloghttp "github.com/CodyLionVivo/duradent/services/api/internal/catalog/adapters/http"
	catalogmysql "github.com/CodyLionVivo/duradent/services/api/internal/catalog/adapters/mysql"
	catalogapp "github.com/CodyLionVivo/duradent/services/api/internal/catalog/application"
	catalogports "github.com/CodyLionVivo/duradent/services/api/internal/catalog/ports"
	identityargon2 "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/argon2"
	identityhttp "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/http"
	identitymysql "github.com/CodyLionVivo/duradent/services/api/internal/identity/adapters/mysql"
	identityapp "github.com/CodyLionVivo/duradent/services/api/internal/identity/application"
	identitydomain "github.com/CodyLionVivo/duradent/services/api/internal/identity/domain"
	identityports "github.com/CodyLionVivo/duradent/services/api/internal/identity/ports"
	inventoryhttp "github.com/CodyLionVivo/duradent/services/api/internal/inventory/adapters/http"
	inventorymysql "github.com/CodyLionVivo/duradent/services/api/internal/inventory/adapters/mysql"
	inventoryapp "github.com/CodyLionVivo/duradent/services/api/internal/inventory/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	ordershttp "github.com/CodyLionVivo/duradent/services/api/internal/orders/adapters/http"
	ordersmysql "github.com/CodyLionVivo/duradent/services/api/internal/orders/adapters/mysql"
	ordersapp "github.com/CodyLionVivo/duradent/services/api/internal/orders/application"
	ordersports "github.com/CodyLionVivo/duradent/services/api/internal/orders/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/auth"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/outbox"
	pricinghttp "github.com/CodyLionVivo/duradent/services/api/internal/pricing/adapters/http"
	pricingmysql "github.com/CodyLionVivo/duradent/services/api/internal/pricing/adapters/mysql"
	pricingapp "github.com/CodyLionVivo/duradent/services/api/internal/pricing/application"
	shippingmysql "github.com/CodyLionVivo/duradent/services/api/internal/shipping/adapters/mysql"
	shippingapp "github.com/CodyLionVivo/duradent/services/api/internal/shipping/application"
)

// Options are the deploy-time choices and external adapters.
type Options struct {
	DB        *sql.DB
	Log       *slog.Logger
	Media     catalogports.MediaStorage
	Providers map[identitydomain.Provider]identityports.OIDCProvider
	Gateway   ordersports.PaymentGateway
	Now       func() time.Time

	AdminEmails []string
	DevLogin    bool
}

type App struct {
	Catalog   *catalogapp.Service
	Pricing   *pricingapp.Service
	Identity  *identityapp.Service
	Inventory *inventoryapp.Service
	Shipping  *shippingapp.Service
	Orders    *ordersapp.Service
}

func Build(o Options) (*App, error) {
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	}
	db := o.DB
	uow := mysql.NewUnitOfWork(db)
	events := outbox.NewWriter(db)

	a := &App{
		Catalog:   catalogapp.NewService(catalogmysql.NewProducts(db), catalogmysql.NewCategories(db), catalogmysql.NewAttributes(db), o.Media, o.Now),
		Pricing:   pricingapp.NewService(pricingmysql.NewPriceLists(db), pricingmysql.NewPrices(db)),
		Inventory: inventoryapp.NewService(inventorymysql.New(db), uow, events, o.Now),
		Shipping:  shippingapp.NewService(shippingmysql.New(db)),
	}
	stores := identitymysql.New(db)
	var err error
	a.Identity, err = identityapp.NewService(identityapp.Deps{
		Accounts: stores.Accounts, Customers: stores.Customers, Addresses: stores.Addresses,
		Sessions: stores.Sessions, States: stores.LoginStates, Hasher: identityargon2.Hasher{},
		Providers: o.Providers, Now: o.Now,
	}, identityapp.Config{AdminEmails: o.AdminEmails, DevLogin: o.DevLogin})
	if err != nil {
		return nil, err
	}
	ost := ordersmysql.New(db)
	a.Orders = ordersapp.NewService(ordersapp.Deps{
		Carts: ost.Carts, Orders: ost.Orders, Payments: ost.Payments, Idempotency: ost.Idempotency,
		UoW: uow, Events: events,
		Catalog: catalogBridge{a.Catalog}, Prices: pricesBridge{a.Pricing}, Stock: stockBridge{a.Inventory},
		Shipping: shippingBridge{a.Shipping}, Customers: customersBridge{a.Identity},
		Gateway: o.Gateway, Log: o.Log, Now: o.Now,
	})
	return a, nil
}

// HTTPOptions configure the API surface.
type HTTPOptions struct {
	AdminToken    string
	WebBaseURL    string
	TrustProxy    bool
	PublicKey     string // Culqi public key, handed to the web
	WebhookSecret string // path secret of the Culqi webhook URL
	BankDetails   string // instructions shown for bank transfers
}

// Handler mounts every route and returns the API handler (without the
// access-log/metrics middleware, which httpserver.Run adds).
func (a *App) Handler(db *sql.DB, log *slog.Logger, o HTTPOptions) http.Handler {
	mux := httpserver.NewMux(map[string]httpserver.Pinger{"mysql": db})
	admin := auth.RequireAdmin(o.AdminToken)
	cataloghttp.Register(mux, a.Catalog, log, admin)
	pricinghttp.Register(mux, a.Pricing, log, admin)
	identityhttp.Register(mux, a.Identity, log, identityhttp.Options{
		WebBaseURL: o.WebBaseURL, TrustProxy: o.TrustProxy, PriceListExists: a.Pricing.PriceListExists, Admin: admin,
	})
	inventoryhttp.Register(mux, a.Inventory, log, admin)
	ordershttp.Register(mux, a.Orders, log, ordershttp.Options{
		Admin: admin, TrustProxy: o.TrustProxy, PublicKey: o.PublicKey, WebhookSecret: o.WebhookSecret, BankDetails: o.BankDetails,
	})
	return auth.Middleware(a.sessionResolver(), log, mux)
}

func (a *App) sessionResolver() auth.Resolver {
	return func(ctx context.Context, token string) (auth.Principal, bool, error) {
		p, err := a.Identity.Authenticate(ctx, token)
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
