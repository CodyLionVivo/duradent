// Package application implements the cart, checkout, online payment with
// Culqi, bank transfers and order fulfilment.
package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/ports"
)

type Deps struct {
	Carts       ports.CartRepository
	Orders      ports.OrderRepository
	Payments    ports.PaymentRepository
	Idempotency ports.Idempotency
	UoW         ports.UnitOfWork
	Events      ports.Events
	Catalog     ports.Catalog
	Prices      ports.Prices
	Stock       ports.Stock
	Shipping    ports.Shipping
	Customers   ports.Customers
	Gateway     ports.PaymentGateway
	Log         *slog.Logger
	Now         func() time.Time
}

type Service struct{ d Deps }

func NewService(d Deps) *Service { return &Service{d: d} }

func (s *Service) GatewayName() string { return s.d.Gateway.Name() }

// --- cart ----------------------------------------------------------------------------

// CartOwner identifies a cart: the signed-in account, or an anonymous key.
type CartOwner struct {
	AccountID id.ID
	Key       string
}

func (o CartOwner) anonymous() bool { return o.AccountID.IsNil() }

func newCartKey() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// load returns the owner's cart, or a new unsaved one.
func (s *Service) load(ctx context.Context, o CartOwner) (*domain.Cart, error) {
	var c *domain.Cart
	var err error
	switch {
	case !o.anonymous():
		c, err = s.d.Carts.ByAccount(ctx, o.AccountID)
	case o.Key != "":
		c, err = s.d.Carts.ByKey(ctx, o.Key)
	default:
		err = apperr.NotFound("orders.cart_not_found", "no cart")
	}
	if apperr.IsKind(err, apperr.KindNotFound) {
		return domain.NewCart(o.AccountID, "", s.d.Now()), nil
	}
	return c, err
}

// CartLine is a cart line enriched for display and checkout.
type CartLine struct {
	ports.VariantInfo
	Qty       int
	UnitPrice *money.Money // nil: not priced in this currency
	LineTotal *money.Money
	Available int
	// Problem is empty when the line can be bought:
	// "unavailable" (unpublished/archived), "not_priced", "insufficient_stock".
	Problem string
}

type CartView struct {
	Key       string // anonymous carts only
	Lines     []CartLine
	Subtotal  money.Money
	ItemCount int
	WeightG   int
	Ready     bool // every line can be bought
}

type CartQuery struct {
	Owner         CartOwner
	PriceListCode string
	Currency      money.Currency
	Locale        locale.Locale
}

func (s *Service) Cart(ctx context.Context, q CartQuery) (CartView, error) {
	c, err := s.load(ctx, q.Owner)
	if err != nil {
		return CartView{}, err
	}
	return s.view(ctx, c, q.PriceListCode, q.Currency, q.Locale)
}

func (s *Service) view(ctx context.Context, c *domain.Cart, listCode string, cur money.Currency, l locale.Locale) (CartView, error) {
	v := CartView{Key: c.Key, Subtotal: money.New(0, cur), Ready: !c.Empty()}
	if c.Empty() {
		return v, nil
	}
	ids := make([]id.ID, len(c.Items))
	lines := make([]ports.LineQty, len(c.Items))
	for i, it := range c.Items {
		ids[i] = it.VariantID
		lines[i] = ports.LineQty{VariantID: it.VariantID, Qty: it.Qty}
	}
	info, err := s.d.Catalog.Variants(ctx, ids, l)
	if err != nil {
		return CartView{}, err
	}
	prices, err := s.d.Prices.UnitPrices(ctx, listCode, cur, lines)
	if err != nil {
		return CartView{}, err
	}
	avail, err := s.d.Stock.Availability(ctx, ids)
	if err != nil {
		return CartView{}, err
	}
	for _, it := range c.Items {
		vi, ok := info[it.VariantID]
		if !ok {
			vi = ports.VariantInfo{VariantID: it.VariantID}
		}
		line := CartLine{VariantInfo: vi, Qty: it.Qty, Available: avail[it.VariantID]}
		if p, ok := prices[it.VariantID]; ok {
			total := p.Mul(int64(it.Qty))
			line.UnitPrice, line.LineTotal = &p, &total
		}
		switch {
		case !vi.Purchasable:
			line.Problem = "unavailable"
		case line.UnitPrice == nil:
			line.Problem = "not_priced"
		case line.Available < it.Qty:
			line.Problem = "insufficient_stock"
		}
		if line.Problem != "" {
			v.Ready = false
		} else {
			v.Subtotal, _ = v.Subtotal.Add(*line.LineTotal)
		}
		v.ItemCount += it.Qty
		v.WeightG += vi.WeightG * it.Qty
		v.Lines = append(v.Lines, line)
	}
	return v, nil
}

// AddToCart adds units of a variant. For anonymous visitors it creates the
// cart on first use and returns its key (to be kept in a cookie).
func (s *Service) AddToCart(ctx context.Context, o CartOwner, variantID id.ID, qty int) (string, error) {
	info, err := s.d.Catalog.Variants(ctx, []id.ID{variantID}, locale.Default)
	if err != nil {
		return "", err
	}
	if !info[variantID].Purchasable {
		return "", apperr.Invalid("orders.item_unavailable", "variant_id", "this product is not available")
	}
	c, err := s.load(ctx, o)
	if err != nil {
		return "", err
	}
	if c.Key == "" && o.anonymous() {
		c.Key = newCartKey()
	}
	if err := c.Add(variantID, qty, s.d.Now()); err != nil {
		return "", err
	}
	return c.Key, s.d.Carts.Save(ctx, c)
}

func (s *Service) SetCartQty(ctx context.Context, o CartOwner, variantID id.ID, qty int) error {
	c, err := s.load(ctx, o)
	if err != nil {
		return err
	}
	if err := c.Set(variantID, qty, s.d.Now()); err != nil {
		return err
	}
	return s.d.Carts.Save(ctx, c)
}

// MergeCart moves an anonymous cart into the account's cart after sign-in.
func (s *Service) MergeCart(ctx context.Context, accountID id.ID, key string) error {
	if key == "" {
		return nil
	}
	anon, err := s.d.Carts.ByKey(ctx, key)
	if apperr.IsKind(err, apperr.KindNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	mine, err := s.load(ctx, CartOwner{AccountID: accountID})
	if err != nil {
		return err
	}
	return s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		mine.Merge(anon, s.d.Now())
		if err := s.d.Carts.Save(ctx, mine); err != nil {
			return err
		}
		return s.d.Carts.Delete(ctx, anon.ID)
	})
}

// --- checkout ------------------------------------------------------------------------

type CheckoutInput struct {
	AccountID      id.ID
	Currency       money.Currency
	AddressID      id.ID
	Pickup         bool
	Method         domain.PaymentMethod
	Notes          string
	IdempotencyKey string
}

type Quote struct {
	Cart     CartView
	Shipping domain.Shipping
	ShipCost money.Money
	Tax      money.Money
	Total    money.Money
	Billing  *domain.Billing
}

// QuoteCheckout prices the account's cart with delivery and IGV.
func (s *Service) QuoteCheckout(ctx context.Context, in CheckoutInput) (Quote, error) {
	cust, err := s.d.Customers.Info(ctx, in.AccountID)
	if err != nil {
		return Quote{}, err
	}
	c, err := s.load(ctx, CartOwner{AccountID: in.AccountID})
	if err != nil {
		return Quote{}, err
	}
	cart, err := s.view(ctx, c, cust.PriceListCode, in.Currency, locale.Default)
	if err != nil {
		return Quote{}, err
	}
	ship, err := s.shippingFor(cust, in)
	if err != nil {
		return Quote{}, err
	}
	zone, cost, err := s.d.Shipping.Quote(ctx, ship.Department, ship.Pickup, cart.WeightG)
	if err != nil {
		return Quote{}, err
	}
	ship.Zone = zone
	tax, total := domain.Totals(cart.Subtotal, cost)
	return Quote{Cart: cart, Shipping: ship, ShipCost: cost, Tax: tax, Total: total, Billing: cust.Billing}, nil
}

func (s *Service) shippingFor(cust ports.CustomerInfo, in CheckoutInput) (domain.Shipping, error) {
	if in.Pickup {
		return domain.Shipping{Pickup: true}, nil
	}
	for _, a := range cust.Addresses {
		if a.ID == in.AddressID {
			return domain.Shipping{
				Recipient: a.Recipient, Line1: a.Line1, Reference: a.Reference, District: a.District,
				Province: a.Province, Department: a.Department, Phone: a.Phone,
			}, nil
		}
	}
	return domain.Shipping{}, apperr.Invalid("orders.address_required", "address_id", "choose a delivery address or store pickup")
}

func requestHash(in CheckoutInput) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%t|%s|%s", in.Currency, in.AddressID, in.Pickup, in.Method, in.Notes))
	return hex.EncodeToString(sum[:])
}

// PlaceOrder turns the cart into an order: it freezes prices, reserves stock
// and records OrderPlaced in one transaction. Retrying with the same
// Idempotency-Key returns the same order instead of creating another.
func (s *Service) PlaceOrder(ctx context.Context, in CheckoutInput) (*domain.Order, error) {
	scope := "place_order:" + in.AccountID.String()
	hash := requestHash(in)
	if in.IdempotencyKey != "" {
		prev, found, err := s.d.Idempotency.Lookup(ctx, scope, in.IdempotencyKey, hash)
		if err != nil {
			return nil, err
		}
		if found {
			return s.d.Orders.ByID(ctx, id.MustParse(prev))
		}
	}

	q, err := s.QuoteCheckout(ctx, in)
	if err != nil {
		return nil, err
	}
	if q.Billing == nil {
		return nil, apperr.Conflict("orders.billing_missing", "billing", "complete your billing details first")
	}
	if len(q.Cart.Lines) == 0 {
		return nil, apperr.Invalid("orders.cart_empty", "items", "the cart is empty")
	}
	for _, l := range q.Cart.Lines {
		if l.Problem != "" {
			return nil, apperr.Conflict("orders.item_"+l.Problem, l.VariantID.String(), "a product in the cart cannot be bought: "+l.SKU)
		}
	}
	cust, err := s.d.Customers.Info(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}

	var order *domain.Order
	err = s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		number, err := s.d.Orders.NextNumber(ctx)
		if err != nil {
			return err
		}
		lines := make([]domain.Line, len(q.Cart.Lines))
		reserve := make([]ports.LineQty, len(q.Cart.Lines))
		for i, l := range q.Cart.Lines {
			lines[i] = domain.Line{VariantID: l.VariantID, SKU: l.SKU, Name: l.ProductName, Options: l.Options, UnitPrice: *l.UnitPrice, Qty: l.Qty}
			reserve[i] = ports.LineQty{VariantID: l.VariantID, Qty: l.Qty}
		}
		now := s.d.Now()
		order, err = domain.NewOrder(domain.NewOrderInput{
			Number: number, AccountID: in.AccountID, Email: cust.Email, Method: in.Method, Currency: in.Currency,
			Lines: lines, Shipping: q.Shipping, ShipCost: q.ShipCost, Billing: *q.Billing, Notes: in.Notes,
		}, now)
		if err != nil {
			return err
		}
		if err := s.d.Orders.Create(ctx, order); err != nil {
			return err
		}
		if err := s.d.Stock.Reserve(ctx, order.ID, reserve); err != nil {
			return err
		}
		if err := s.record(ctx, order, "OrderPlaced", map[string]any{
			"order_id": order.ID, "number": order.Number, "account_id": order.AccountID, "email": order.Email,
			"total": order.Total.Decimal(), "currency": order.Currency, "payment_method": order.Method,
			"payment_deadline": order.PaymentDeadline, "items": reserve,
		}); err != nil {
			return err
		}
		c, err := s.load(ctx, CartOwner{AccountID: in.AccountID})
		if err != nil {
			return err
		}
		c.Clear(now)
		if err := s.d.Carts.Save(ctx, c); err != nil {
			return err
		}
		if in.IdempotencyKey != "" {
			return s.d.Idempotency.Store(ctx, scope, in.IdempotencyKey, hash, order.ID.String(), now.Add(24*time.Hour))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

func (s *Service) record(ctx context.Context, o *domain.Order, event string, payload any) error {
	return s.d.Events.Record(ctx, "order", o.ID, event, payload, s.d.Now())
}

// --- online payment --------------------------------------------------------------------

// PayWithCulqi charges the order with a token from Culqi Checkout.
func (s *Service) PayWithCulqi(ctx context.Context, accountID id.ID, number, sourceID, clientIP string) (*domain.Order, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" || len(sourceID) > 100 {
		return nil, apperr.Invalid("orders.token_invalid", "token", "missing payment token")
	}
	o, err := s.MyOrder(ctx, accountID, number)
	if err != nil {
		return nil, err
	}

	// 1. Mark the charge as in flight (short transaction).
	err = s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		locked, err := s.d.Orders.Lock(ctx, o.ID)
		if err != nil {
			return err
		}
		if err := locked.BeginCharge(sourceID, s.d.Now()); err != nil {
			return err
		}
		o = locked
		return s.d.Orders.Update(ctx, locked)
	})
	if err != nil {
		return nil, err
	}

	// 2. Charge outside any transaction: it can take seconds.
	charge, err := s.d.Gateway.Charge(ctx, ports.ChargeRequest{
		AmountMinor: o.Total.Minor, Currency: o.Currency, Email: o.Email, SourceID: sourceID,
		Description: "Pedido " + o.Number, OrderID: o.ID, OrderNumber: o.Number, ClientIP: clientIP,
	})
	if err != nil {
		// Unknown outcome: keep the in-flight flag (it expires by itself) so
		// the webhook can settle the order if the charge did go through.
		s.d.Log.ErrorContext(ctx, "culqi charge failed", "order", o.Number, "error", err)
		return nil, apperr.Conflict("orders.payment_unknown", "token", "we could not confirm the payment; check your order in a few minutes before retrying")
	}
	if !charge.Succeeded {
		return nil, s.declined(ctx, o.ID, charge)
	}
	// 3. Settle.
	return s.confirmCharge(ctx, o.ID, charge, "culqi")
}

func (s *Service) declined(ctx context.Context, orderID id.ID, c ports.Charge) error {
	err := s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		o, err := s.d.Orders.Lock(ctx, orderID)
		if err != nil {
			return err
		}
		now := s.d.Now()
		o.EndCharge(now)
		if err := s.d.Orders.Update(ctx, o); err != nil {
			return err
		}
		if err := s.d.Payments.Add(ctx, domain.Payment{
			ID: id.New(), OrderID: o.ID, Method: domain.MethodCulqi, Status: domain.PaymentFailed, Amount: o.Total,
			ProviderRef: c.ID, SourceType: c.SourceType, FailureCode: c.Code, FailureMessage: c.UserMessage, CreatedAt: now,
		}); err != nil {
			return err
		}
		return s.record(ctx, o, "PaymentFailed", map[string]any{"order_id": o.ID, "number": o.Number, "code": c.Code})
	})
	if err != nil {
		return err
	}
	msg := c.UserMessage
	if msg == "" {
		msg = "the payment was declined"
	}
	return apperr.Invalid("orders.payment_declined", c.Code, msg)
}

// confirmCharge marks the order paid for a succeeded charge. It is safe to
// call twice for the same charge (API response and webhook).
func (s *Service) confirmCharge(ctx context.Context, orderID id.ID, c ports.Charge, actor string) (*domain.Order, error) {
	var out *domain.Order
	err := s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		o, err := s.d.Orders.Lock(ctx, orderID)
		if err != nil {
			return err
		}
		if done, err := s.d.Payments.ConfirmedByRef(ctx, c.ID); err != nil || done {
			out = o
			return err
		}
		if c.AmountMinor != o.Total.Minor || c.Currency != o.Currency {
			s.d.Log.ErrorContext(ctx, "charge amount mismatch", "order", o.Number, "charge", c.ID, "amount", c.AmountMinor)
			return apperr.Conflict("orders.payment_mismatch", "token", "the charged amount does not match the order")
		}
		now := s.d.Now()
		if o.Status == domain.StatusCancelled {
			// Paid after the window closed: record it so an admin refunds it.
			s.d.Log.ErrorContext(ctx, "payment received for a cancelled order: refund needed", "order", o.Number, "charge", c.ID)
		} else if err := o.MarkPaid(actor, c.ID, now); err != nil {
			return err
		}
		if err := s.d.Orders.Update(ctx, o); err != nil {
			return err
		}
		p := domain.Payment{
			ID: id.New(), OrderID: o.ID, Method: domain.MethodCulqi, Status: domain.PaymentConfirmed, Amount: o.Total,
			ProviderRef: c.ID, SourceType: c.SourceType, CreatedAt: now,
		}
		if err := s.d.Payments.Add(ctx, p); err != nil {
			return err
		}
		if o.Status == domain.StatusPaid {
			if err := s.d.Stock.Confirm(ctx, o.ID); err != nil {
				return err
			}
		}
		out = o
		return s.record(ctx, o, "PaymentConfirmed", map[string]any{
			"order_id": o.ID, "number": o.Number, "payment_id": p.ID, "method": p.Method, "source": p.SourceType,
			"amount": o.Total.Decimal(), "currency": o.Currency, "provider_ref": c.ID, "order_status": o.Status,
		})
	})
	return out, err
}

// Reconcile settles an order from a Culqi webhook. The webhook body is not
// trusted (Culqi does not sign it): the charge is re-read from Culqi's API.
func (s *Service) Reconcile(ctx context.Context, chargeID string) error {
	if !strings.HasPrefix(chargeID, "chr_") || len(chargeID) > 100 {
		return apperr.Invalid("orders.charge_invalid", "id", "not a charge id")
	}
	if done, err := s.d.Payments.ConfirmedByRef(ctx, chargeID); err != nil || done {
		return err
	}
	c, err := s.d.Gateway.GetCharge(ctx, chargeID)
	if err != nil {
		return err
	}
	if !c.Succeeded {
		return nil
	}
	orderID, err := id.Parse(c.OrderID)
	if err != nil {
		return apperr.Invalid("orders.charge_without_order", "metadata", "charge has no order id")
	}
	_, err = s.confirmCharge(ctx, orderID, c, "culqi-webhook")
	return err
}

// --- customer views ------------------------------------------------------------------

func (s *Service) MyOrders(ctx context.Context, accountID id.ID, limit, offset int) ([]*domain.Order, int, error) {
	return s.d.Orders.List(ctx, ports.OrderFilter{AccountID: accountID, Limit: limit, Offset: offset})
}

// ByNumber returns any order (for other contexts; not exposed to customers).
func (s *Service) ByNumber(ctx context.Context, number string) (*domain.Order, error) {
	return s.d.Orders.ByNumber(ctx, number)
}

// MyOrder returns the order only if it belongs to the account.
func (s *Service) MyOrder(ctx context.Context, accountID id.ID, number string) (*domain.Order, error) {
	o, err := s.d.Orders.ByNumber(ctx, number)
	if err != nil {
		return nil, err
	}
	if o.AccountID != accountID {
		return nil, apperr.NotFound("orders.order_not_found", "order not found")
	}
	return o, nil
}

func (s *Service) CancelMine(ctx context.Context, accountID id.ID, number string) (*domain.Order, error) {
	o, err := s.MyOrder(ctx, accountID, number)
	if err != nil {
		return nil, err
	}
	return s.cancel(ctx, o.ID, "cancelled by the customer", "customer")
}

func (s *Service) cancel(ctx context.Context, orderID id.ID, reason, actor string) (*domain.Order, error) {
	var out *domain.Order
	err := s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		o, err := s.d.Orders.Lock(ctx, orderID)
		if err != nil {
			return err
		}
		if o.Status == domain.StatusCancelled {
			out = o
			return nil
		}
		if err := o.Cancel(reason, actor, s.d.Now()); err != nil {
			return err
		}
		if err := s.d.Orders.Update(ctx, o); err != nil {
			return err
		}
		if err := s.d.Stock.Release(ctx, o.ID); err != nil {
			return err
		}
		out = o
		return s.record(ctx, o, "OrderCancelled", map[string]any{"order_id": o.ID, "number": o.Number, "reason": reason, "by": actor})
	})
	return out, err
}

// ExpireOverdue cancels unpaid orders whose payment window closed and
// releases their stock. The worker runs it every minute.
func (s *Service) ExpireOverdue(ctx context.Context) (int, error) {
	now := s.d.Now()
	ids, err := s.d.Orders.Overdue(ctx, now, 100)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, oid := range ids {
		o, err := s.d.Orders.ByID(ctx, oid)
		if err != nil || !o.Overdue(now) {
			continue
		}
		if _, err := s.cancel(ctx, oid, "payment window expired", "system"); err != nil {
			s.d.Log.ErrorContext(ctx, "expire order", "order", o.Number, "error", err)
			continue
		}
		n++
	}
	return n, nil
}

// --- admin ---------------------------------------------------------------------------

type OrderDetail struct {
	Order    *domain.Order
	Payments []domain.Payment
	History  []domain.StatusChange
}

func (s *Service) Orders(ctx context.Context, f ports.OrderFilter) ([]*domain.Order, int, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	return s.d.Orders.List(ctx, f)
}

func (s *Service) Detail(ctx context.Context, orderID id.ID) (OrderDetail, error) {
	o, err := s.d.Orders.ByID(ctx, orderID)
	if err != nil {
		return OrderDetail{}, err
	}
	pays, err := s.d.Payments.ByOrder(ctx, orderID)
	if err != nil {
		return OrderDetail{}, err
	}
	hist, err := s.d.Orders.History(ctx, orderID)
	if err != nil {
		return OrderDetail{}, err
	}
	return OrderDetail{Order: o, Payments: pays, History: hist}, nil
}

func adminActor(adminID id.ID) string {
	if adminID.IsNil() {
		return "admin"
	}
	return "admin:" + adminID.String()
}

// ConfirmTransfer records a bank transfer an admin saw in the bank account.
func (s *Service) ConfirmTransfer(ctx context.Context, adminID, orderID id.ID, reference string) (*domain.Order, error) {
	reference = strings.TrimSpace(reference)
	if reference == "" || len(reference) > 60 {
		return nil, apperr.Invalid("orders.reference_required", "reference", "enter the bank operation number")
	}
	var out *domain.Order
	err := s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		o, err := s.d.Orders.Lock(ctx, orderID)
		if err != nil {
			return err
		}
		if o.Method != domain.MethodBankTransfer {
			return apperr.Conflict("orders.not_transfer", "payment_method", "this order is paid online")
		}
		now := s.d.Now()
		if err := o.MarkPaid(adminActor(adminID), reference, now); err != nil {
			return err
		}
		if err := s.d.Orders.Update(ctx, o); err != nil {
			return err
		}
		p := domain.Payment{ID: id.New(), OrderID: o.ID, Method: domain.MethodBankTransfer, Status: domain.PaymentConfirmed,
			Amount: o.Total, Reference: reference, ConfirmedBy: adminID, CreatedAt: now}
		if err := s.d.Payments.Add(ctx, p); err != nil {
			return err
		}
		if err := s.d.Stock.Confirm(ctx, o.ID); err != nil {
			return err
		}
		out = o
		return s.record(ctx, o, "PaymentConfirmed", map[string]any{
			"order_id": o.ID, "number": o.Number, "payment_id": p.ID, "method": p.Method,
			"amount": o.Total.Decimal(), "currency": o.Currency, "reference": reference, "order_status": o.Status,
		})
	})
	return out, err
}

func (s *Service) mutate(ctx context.Context, orderID id.ID, fn func(*domain.Order, time.Time) error, event func(*domain.Order) (string, any)) (*domain.Order, error) {
	var out *domain.Order
	err := s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		o, err := s.d.Orders.Lock(ctx, orderID)
		if err != nil {
			return err
		}
		if err := fn(o, s.d.Now()); err != nil {
			return err
		}
		if err := s.d.Orders.Update(ctx, o); err != nil {
			return err
		}
		out = o
		if event != nil {
			name, payload := event(o)
			return s.record(ctx, o, name, payload)
		}
		return nil
	})
	return out, err
}

func (s *Service) StartPreparing(ctx context.Context, adminID, orderID id.ID) (*domain.Order, error) {
	return s.mutate(ctx, orderID, func(o *domain.Order, now time.Time) error { return o.StartPreparing(adminActor(adminID), now) }, nil)
}

func (s *Service) Ship(ctx context.Context, adminID, orderID id.ID, carrier, tracking string) (*domain.Order, error) {
	return s.mutate(ctx, orderID,
		func(o *domain.Order, now time.Time) error { return o.Ship(carrier, tracking, adminActor(adminID), now) },
		func(o *domain.Order) (string, any) {
			return "OrderShipped", map[string]any{"order_id": o.ID, "number": o.Number, "carrier": o.Carrier, "tracking": o.Tracking, "email": o.Email}
		})
}

func (s *Service) Deliver(ctx context.Context, adminID, orderID id.ID) (*domain.Order, error) {
	return s.mutate(ctx, orderID,
		func(o *domain.Order, now time.Time) error { return o.Deliver(adminActor(adminID), now) },
		func(o *domain.Order) (string, any) {
			return "OrderDelivered", map[string]any{"order_id": o.ID, "number": o.Number}
		})
}

// Refund cancels a paid order that has not shipped and returns the money:
// Culqi charges are refunded through Culqi; bank transfers are returned by
// hand and the admin enters the bank operation number. Stock goes back on
// hand and OrderRefunded triggers the credit note and the customer email.
func (s *Service) Refund(ctx context.Context, adminID, orderID id.ID, reason, reference string) (*domain.Order, error) {
	reason, reference = strings.TrimSpace(reason), strings.TrimSpace(reference)
	if reason == "" {
		return nil, apperr.Invalid("orders.reason_required", "reason", "enter the reason for the refund")
	}
	o, err := s.d.Orders.ByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	pays, err := s.d.Payments.ByOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	var paid *domain.Payment
	for i := range pays {
		if pays[i].Status == domain.PaymentConfirmed {
			paid = &pays[i]
		}
	}
	if err := o.CanRefund(paid != nil); err != nil {
		return nil, err
	}

	ref := reference
	if paid.Method == domain.MethodCulqi {
		// Outside the transaction: a second click is rejected by Culqi
		// (the charge has nothing left to refund).
		if ref, err = s.d.Gateway.Refund(ctx, ports.RefundRequest{ChargeID: paid.ProviderRef, AmountMinor: paid.Amount.Minor}); err != nil {
			s.d.Log.ErrorContext(ctx, "culqi refund", "order", o.Number, "charge", paid.ProviderRef, "error", err)
			return nil, apperr.Conflict("orders.refund_failed", "", "Culqi did not accept the refund; try again or refund it from the Culqi panel")
		}
	} else if ref == "" || len(ref) > 60 {
		return nil, apperr.Invalid("orders.reference_required", "reference", "enter the bank operation number of the returned transfer")
	}

	var out *domain.Order
	err = s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		locked, err := s.d.Orders.Lock(ctx, orderID)
		if err != nil {
			return err
		}
		now := s.d.Now()
		if err := locked.Refund(reason, ref, adminActor(adminID), now); err != nil {
			return err
		}
		if err := s.d.Orders.Update(ctx, locked); err != nil {
			return err
		}
		p := domain.Payment{ID: id.New(), OrderID: orderID, Method: paid.Method, Status: domain.PaymentRefunded,
			Amount: paid.Amount, ProviderRef: ref, Reference: reference, ConfirmedBy: adminID, CreatedAt: now}
		if paid.Method == domain.MethodBankTransfer {
			p.ProviderRef = ""
		}
		if err := s.d.Payments.Add(ctx, p); err != nil {
			return err
		}
		if err := s.d.Stock.Restock(ctx, orderID); err != nil {
			return err
		}
		out = locked
		return s.record(ctx, locked, "OrderRefunded", map[string]any{
			"order_id": locked.ID, "number": locked.Number, "amount": paid.Amount.Decimal(), "currency": paid.Amount.Currency,
			"method": paid.Method, "refund_ref": ref, "reason": reason,
		})
	})
	if err != nil {
		// Money may already be back with the customer: make it visible.
		s.d.Log.ErrorContext(ctx, "refund not recorded", "order", o.Number, "refund_ref", ref, "error", err)
		return nil, err
	}
	return out, nil
}

func (s *Service) CancelByAdmin(ctx context.Context, adminID, orderID id.ID, reason string) (*domain.Order, error) {
	if strings.TrimSpace(reason) == "" {
		reason = "cancelled by Duradent"
	}
	return s.cancel(ctx, orderID, reason, adminActor(adminID))
}
