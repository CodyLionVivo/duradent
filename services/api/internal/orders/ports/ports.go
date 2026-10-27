// Package ports declares what the orders context needs: its own persistence,
// read access to other contexts (catalog, pricing, inventory, shipping,
// identity) and the payment gateway. The composition root wires them.
package ports

import (
	"context"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/domain"
)

// --- persistence (all methods join the transaction carried by ctx) -------------

type CartRepository interface {
	ByAccount(ctx context.Context, accountID id.ID) (*domain.Cart, error) // NotFound if none
	ByKey(ctx context.Context, key string) (*domain.Cart, error)          // NotFound if none
	Save(ctx context.Context, c *domain.Cart) error
	Delete(ctx context.Context, cartID id.ID) error
}

type OrderFilter struct {
	AccountID id.ID // id.Nil: any account
	Statuses  []domain.Status
	Query     string // number, email or document
	Limit     int
	Offset    int
}

type OrderRepository interface {
	Create(ctx context.Context, o *domain.Order) error
	// Update persists status fields and appends o.History.
	Update(ctx context.Context, o *domain.Order) error
	ByID(ctx context.Context, orderID id.ID) (*domain.Order, error)
	ByNumber(ctx context.Context, number string) (*domain.Order, error)
	// Lock loads the order FOR UPDATE inside the current transaction.
	Lock(ctx context.Context, orderID id.ID) (*domain.Order, error)
	List(ctx context.Context, f OrderFilter) ([]*domain.Order, int, error)
	History(ctx context.Context, orderID id.ID) ([]domain.StatusChange, error)
	// Overdue returns unpaid orders whose payment window closed before t.
	Overdue(ctx context.Context, t time.Time, limit int) ([]id.ID, error)
	NextNumber(ctx context.Context) (string, error)
}

type PaymentRepository interface {
	Add(ctx context.Context, p domain.Payment) error
	ByOrder(ctx context.Context, orderID id.ID) ([]domain.Payment, error)
	// ConfirmedByRef reports whether a charge was already recorded as confirmed.
	ConfirmedByRef(ctx context.Context, providerRef string) (bool, error)
}

// Idempotency remembers the result of a request by its Idempotency-Key.
type Idempotency interface {
	// Lookup returns the stored result id; found=false when unknown.
	// A key reused with a different request body is a Conflict.
	Lookup(ctx context.Context, scope, key, requestHash string) (resultID string, found bool, err error)
	Store(ctx context.Context, scope, key, requestHash, resultID string, expires time.Time) error
}

type UnitOfWork interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type Events interface {
	Record(ctx context.Context, aggregateType string, aggregateID id.ID, eventType string, payload any, at time.Time) error
}

// --- other contexts ------------------------------------------------------------

// VariantInfo is what the cart and orders need to know about a variant.
type VariantInfo struct {
	VariantID   id.ID
	SKU         string
	ProductName string
	ProductSlug string
	Options     string // human-readable option values
	ImageURL    string
	WeightG     int
	Purchasable bool // product published and variant not archived
}

type Catalog interface {
	Variants(ctx context.Context, ids []id.ID, l locale.Locale) (map[id.ID]VariantInfo, error)
}

type LineQty struct {
	VariantID id.ID
	Qty       int
}

type Prices interface {
	// UnitPrices prices each line for its quantity (volume tiers) from the
	// given list (falling back to the public list). Unpriced lines are absent.
	UnitPrices(ctx context.Context, listCode string, c money.Currency, lines []LineQty) (map[id.ID]money.Money, error)
}

type Stock interface {
	Availability(ctx context.Context, ids []id.ID) (map[id.ID]int, error)
	Reserve(ctx context.Context, orderID id.ID, lines []LineQty) error
	Confirm(ctx context.Context, orderID id.ID) error
	Release(ctx context.Context, orderID id.ID) error
	// Restock puts sold units back on hand (refund).
	Restock(ctx context.Context, orderID id.ID) error
}

type Shipping interface {
	Quote(ctx context.Context, department string, pickup bool, weightG int) (zone string, amount money.Money, err error)
}

type Address struct {
	ID         id.ID
	Recipient  string
	Line1      string
	Reference  string
	District   string
	Province   string
	Department string
	Phone      string
}

type CustomerInfo struct {
	Email         string
	Name          string
	PriceListCode string
	Billing       *domain.Billing // nil until the profile is filled in
	Addresses     []Address
}

type Customers interface {
	Info(ctx context.Context, accountID id.ID) (CustomerInfo, error)
}

// --- payments -----------------------------------------------------------------

type ChargeRequest struct {
	AmountMinor int64
	Currency    money.Currency
	Email       string
	SourceID    string // Culqi token: tkn_… (card) or ype_… (Yape)
	Description string
	OrderID     id.ID
	OrderNumber string
	ClientIP    string
}

type Charge struct {
	ID          string
	Succeeded   bool
	AmountMinor int64
	Currency    money.Currency
	OrderID     string // from metadata, for reconciliation
	SourceType  string // card, yape
	Code        string // decline or error code when not succeeded
	UserMessage string // shown to the customer (Culqi writes it in Spanish)
}

type PaymentGateway interface {
	// Charge returns Succeeded=false with Code/UserMessage for declines;
	// err is reserved for transport or unexpected failures.
	Charge(ctx context.Context, r ChargeRequest) (Charge, error)
	// GetCharge re-reads a charge from the provider (webhook reconciliation).
	GetCharge(ctx context.Context, chargeID string) (Charge, error)
	// Refund returns the money of a confirmed charge and gives the
	// provider's refund id.
	Refund(ctx context.Context, r RefundRequest) (string, error)
	// Name is "culqi" or "fake" (local development without keys).
	Name() string
}

type RefundRequest struct {
	ChargeID    string
	AmountMinor int64
}
