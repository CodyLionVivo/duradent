package domain

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

type Status string

const (
	StatusPendingPayment Status = "pending_payment"
	StatusPaid           Status = "paid"
	StatusPreparing      Status = "preparing"
	StatusShipped        Status = "shipped"
	StatusDelivered      Status = "delivered"
	StatusCancelled      Status = "cancelled"
)

func ParseStatus(s string) (Status, error) {
	switch st := Status(s); st {
	case StatusPendingPayment, StatusPaid, StatusPreparing, StatusShipped, StatusDelivered, StatusCancelled:
		return st, nil
	}
	return "", apperr.Invalid("orders.status_invalid", "status", "unknown status")
}

type PaymentMethod string

const (
	MethodCulqi        PaymentMethod = "culqi"         // card or Yape through Culqi Checkout
	MethodBankTransfer PaymentMethod = "bank_transfer" // confirmed by an admin
)

func ParseMethod(s string) (PaymentMethod, error) {
	switch m := PaymentMethod(s); m {
	case MethodCulqi, MethodBankTransfer:
		return m, nil
	}
	return "", apperr.Invalid("orders.payment_method_invalid", "payment_method", "choose culqi or bank_transfer")
}

// IGVPercent is Peru's general sales tax. Catalog prices exclude it; the
// order adds it on top of goods and delivery.
const IGVPercent = 18

// Payment windows: unpaid orders are cancelled and their stock released.
const (
	CulqiPaymentWindow    = 2 * time.Hour
	TransferPaymentWindow = 48 * time.Hour
	chargeLockWindow      = 90 * time.Second
	// YapeMaxPEN is Culqi's per-payment cap for Yape (soles only).
	YapeMaxPEN = 2000_00
)

// Line is a snapshot of what was bought: later catalog or price changes never
// alter an existing order.
type Line struct {
	VariantID id.ID
	SKU       string
	Name      string
	Options   string // "Slot .022 / Arcada Superior"
	UnitPrice money.Money
	Qty       int
	LineTotal money.Money
}

// Billing is the invoicing snapshot taken from the customer's profile.
type Billing struct {
	CustomerType   string `json:"customer_type"`
	DocumentType   string `json:"document_type"`
	DocumentNumber string `json:"document_number"`
	BusinessName   string `json:"business_name"`
	InvoiceType    string `json:"invoice_type"` // 01 factura, 03 boleta
}

// Shipping is where the order goes (snapshot of the address) or pickup.
type Shipping struct {
	Pickup     bool   `json:"pickup"`
	Zone       string `json:"zone"`
	Recipient  string `json:"recipient,omitempty"`
	Line1      string `json:"line1,omitempty"`
	Reference  string `json:"reference,omitempty"`
	District   string `json:"district,omitempty"`
	Province   string `json:"province,omitempty"`
	Department string `json:"department,omitempty"`
	Phone      string `json:"phone,omitempty"`
}

type StatusChange struct {
	From  Status
	To    Status
	Actor string // "customer", "admin:<id>", "system", "culqi"
	Note  string
	At    time.Time
}

type Order struct {
	ID              id.ID
	Number          string // human-friendly, e.g. DD-000123
	AccountID       id.ID
	Email           string
	Status          Status
	Method          PaymentMethod
	Currency        money.Currency
	Lines           []Line
	Subtotal        money.Money
	ShippingAmount  money.Money
	Tax             money.Money
	Total           money.Money
	Billing         Billing
	Shipping        Shipping
	Notes           string
	PaymentDeadline time.Time
	ChargingUntil   *time.Time // set while a card charge is in flight
	Carrier         string
	Tracking        string
	CancelReason    string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	PaidAt          *time.Time
	ShippedAt       *time.Time
	DeliveredAt     *time.Time
	CancelledAt     *time.Time
	History         []StatusChange // changes made in this unit of work (to persist)
}

// Totals computes IGV on goods plus delivery, rounding half up to the céntimo.
func Totals(subtotal, shipping money.Money) (tax, total money.Money) {
	base := subtotal.Minor + shipping.Minor
	taxMinor := (base*IGVPercent + 50) / 100
	return money.New(taxMinor, subtotal.Currency), money.New(base+taxMinor, subtotal.Currency)
}

type NewOrderInput struct {
	Number    string
	AccountID id.ID
	Email     string
	Method    PaymentMethod
	Currency  money.Currency
	Lines     []Line
	Shipping  Shipping
	ShipCost  money.Money
	Billing   Billing
	Notes     string
}

func NewOrder(in NewOrderInput, now time.Time) (*Order, error) {
	if len(in.Lines) == 0 {
		return nil, apperr.Invalid("orders.cart_empty", "items", "the cart is empty")
	}
	if in.Billing.DocumentNumber == "" {
		return nil, apperr.Conflict("orders.billing_missing", "billing", "complete your billing details first")
	}
	notes := strings.TrimSpace(in.Notes)
	if utf8.RuneCountInString(notes) > 500 {
		return nil, apperr.Invalid("orders.notes_too_long", "notes", "notes can have at most 500 characters")
	}
	subtotal := money.New(0, in.Currency)
	for i, l := range in.Lines {
		if l.UnitPrice.Currency != in.Currency || !l.UnitPrice.IsPositive() || l.Qty <= 0 {
			return nil, apperr.Invalid("orders.line_invalid", "items", "invalid line "+l.SKU)
		}
		in.Lines[i].LineTotal = l.UnitPrice.Mul(int64(l.Qty))
		subtotal, _ = subtotal.Add(in.Lines[i].LineTotal)
	}
	if in.ShipCost.Currency != in.Currency {
		return nil, apperr.Invalid("orders.currency_mismatch", "shipping", "shipping must be quoted in the order currency")
	}
	tax, total := Totals(subtotal, in.ShipCost)
	window := CulqiPaymentWindow
	if in.Method == MethodBankTransfer {
		window = TransferPaymentWindow
	}
	o := &Order{
		ID: id.New(), Number: in.Number, AccountID: in.AccountID, Email: in.Email,
		Status: StatusPendingPayment, Method: in.Method, Currency: in.Currency,
		Lines: in.Lines, Subtotal: subtotal, ShippingAmount: in.ShipCost, Tax: tax, Total: total,
		Billing: in.Billing, Shipping: in.Shipping, Notes: notes,
		PaymentDeadline: now.Add(window), CreatedAt: now, UpdatedAt: now,
	}
	o.History = append(o.History, StatusChange{To: StatusPendingPayment, Actor: "customer", At: now})
	return o, nil
}

func (o *Order) transition(to Status, actor, note string, now time.Time) {
	o.History = append(o.History, StatusChange{From: o.Status, To: to, Actor: actor, Note: note, At: now})
	o.Status = to
	o.UpdatedAt = now
}

func badTransition(o *Order, to Status) error {
	return apperr.Conflict("orders.invalid_transition", "status", "cannot go from "+string(o.Status)+" to "+string(to))
}

// BeginCharge marks a card charge as in flight so a second tab or a retry
// cannot charge the customer twice. sourceID is the Culqi token (tkn_… or ype_…).
func (o *Order) BeginCharge(sourceID string, now time.Time) error {
	if o.Status != StatusPendingPayment || o.Method != MethodCulqi {
		return apperr.Conflict("orders.not_payable", "status", "this order cannot be paid online")
	}
	if !now.Before(o.PaymentDeadline) {
		return apperr.Conflict("orders.payment_expired", "status", "the payment window for this order has closed")
	}
	if o.ChargingUntil != nil && now.Before(*o.ChargingUntil) {
		return apperr.Conflict("orders.payment_in_progress", "status", "a payment for this order is already being processed")
	}
	if strings.HasPrefix(sourceID, "ype_") && (o.Currency != money.PEN || o.Total.Minor > YapeMaxPEN) {
		return apperr.Invalid("orders.yape_limit", "token", "Yape accepts payments in soles up to S/ 2,000")
	}
	until := now.Add(chargeLockWindow)
	o.ChargingUntil = &until
	return nil
}

// EndCharge clears the in-flight flag after a failed charge.
func (o *Order) EndCharge(now time.Time) {
	o.ChargingUntil = nil
	o.UpdatedAt = now
}

func (o *Order) MarkPaid(actor, note string, now time.Time) error {
	if o.Status == StatusPaid {
		return nil // idempotent: a webhook may confirm what the API already did
	}
	if o.Status != StatusPendingPayment {
		return badTransition(o, StatusPaid)
	}
	o.ChargingUntil = nil
	o.PaidAt = &now
	o.transition(StatusPaid, actor, note, now)
	return nil
}

func (o *Order) StartPreparing(actor string, now time.Time) error {
	if o.Status != StatusPaid {
		return badTransition(o, StatusPreparing)
	}
	o.transition(StatusPreparing, actor, "", now)
	return nil
}

func (o *Order) Ship(carrier, tracking, actor string, now time.Time) error {
	if o.Status != StatusPaid && o.Status != StatusPreparing {
		return badTransition(o, StatusShipped)
	}
	carrier, tracking = strings.TrimSpace(carrier), strings.TrimSpace(tracking)
	if !o.Shipping.Pickup && carrier == "" {
		return apperr.Invalid("orders.carrier_required", "carrier", "enter the carrier")
	}
	if len(carrier) > 60 || len(tracking) > 80 {
		return apperr.Invalid("orders.tracking_too_long", "tracking", "carrier or tracking number too long")
	}
	o.Carrier, o.Tracking = carrier, tracking
	o.ShippedAt = &now
	o.transition(StatusShipped, actor, tracking, now)
	return nil
}

func (o *Order) Deliver(actor string, now time.Time) error {
	if o.Status != StatusShipped {
		return badTransition(o, StatusDelivered)
	}
	o.DeliveredAt = &now
	o.transition(StatusDelivered, actor, "", now)
	return nil
}

// Cancel applies to unpaid orders. Refunds of paid orders come with phase 5.
func (o *Order) Cancel(reason, actor string, now time.Time) error {
	if o.Status == StatusCancelled {
		return nil
	}
	if o.Status != StatusPendingPayment {
		return apperr.Conflict("orders.cannot_cancel_paid", "status", "paid orders are cancelled with a refund (contact us)")
	}
	if o.ChargingUntil != nil && now.Before(*o.ChargingUntil) {
		return apperr.Conflict("orders.payment_in_progress", "status", "a payment for this order is being processed")
	}
	reason = strings.TrimSpace(reason)
	if utf8.RuneCountInString(reason) > 200 {
		reason = string([]rune(reason)[:200])
	}
	o.CancelReason = reason
	o.CancelledAt = &now
	o.transition(StatusCancelled, actor, reason, now)
	return nil
}

// Overdue reports whether an unpaid order passed its payment window.
func (o *Order) Overdue(now time.Time) bool {
	return o.Status == StatusPendingPayment && !now.Before(o.PaymentDeadline) &&
		(o.ChargingUntil == nil || !now.Before(*o.ChargingUntil))
}

// --- payments ------------------------------------------------------------------------

type PaymentStatus string

const (
	PaymentConfirmed PaymentStatus = "confirmed"
	PaymentFailed    PaymentStatus = "failed"
)

// Payment records every attempt to settle an order.
type Payment struct {
	ID             id.ID
	OrderID        id.ID
	Method         PaymentMethod
	Status         PaymentStatus
	Amount         money.Money
	ProviderRef    string // Culqi charge id
	SourceType     string // card, yape
	FailureCode    string
	FailureMessage string
	Reference      string // bank transfer operation number
	ConfirmedBy    id.ID  // admin who confirmed a transfer
	CreatedAt      time.Time
}
