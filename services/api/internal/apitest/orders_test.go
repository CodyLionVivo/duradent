package apitest

import (
	"context"
	"net/http"
	"testing"

	"github.com/CodyLionVivo/duradent/services/api/internal/platform/httpserver"
)

type cartResp struct {
	Key   string `json:"key"`
	Lines []struct {
		VariantID string  `json:"variant_id"`
		Qty       int     `json:"qty"`
		UnitPrice *string `json:"unit_price"`
		Problem   string  `json:"problem"`
		Options   string  `json:"options"`
	} `json:"lines"`
	Subtotal string `json:"subtotal"`
	Ready    bool   `json:"ready"`
}

type orderResp struct {
	ID             string `json:"id"`
	Number         string `json:"number"`
	Status         string `json:"status"`
	Total          string `json:"total"`
	Subtotal       string `json:"subtotal"`
	ShippingAmount string `json:"shipping_amount"`
	Tax            string `json:"tax"`
	PaymentPending bool   `json:"payment_in_progress"`
	PaymentMethod  string `json:"payment_method"`
	Lines          []struct{ SKU, Name, Options string }
	Billing        struct {
		InvoiceType string `json:"invoice_type"`
	}
	Shipping struct{ Zone string }
}

// shop prepares a product with two variants (5 units and none), public prices,
// demo shipping rates (migration) and a customer with billing and a Lima address.
type shop struct {
	s         server
	v1, v2    string // variant ids
	customer  client
	accountID string
	addressID string
}

func newShop(t *testing.T) shop {
	t.Helper()
	s := newServer(t)
	s.must(201, "POST", "/v1/admin/catalog/attributes", map[string]any{"code": "slot", "names": map[string]string{"es": "Slot"}}, nil)
	for _, v := range []string{"018", "022"} {
		s.must(201, "POST", "/v1/admin/catalog/attributes/slot/values", map[string]any{"code": v, "labels": map[string]string{"es": "." + v}}, nil)
	}
	var p struct {
		ID       string
		Variants []struct{ ID string }
	}
	s.must(201, "POST", "/v1/admin/catalog/products", map[string]any{"slug": "kit-roth", "texts": map[string]any{"es": map[string]string{"name": "Kit Roth"}}}, &p)
	s.must(201, "POST", "/v1/admin/catalog/products/"+p.ID+"/variants", map[string]any{"sku": "KIT-018", "weight_g": 500, "options": map[string]string{"slot": "018"}}, &p)
	s.must(201, "POST", "/v1/admin/catalog/products/"+p.ID+"/variants", map[string]any{"sku": "KIT-022", "weight_g": 500, "options": map[string]string{"slot": "022"}}, &p)
	s.must(200, "POST", "/v1/admin/catalog/products/"+p.ID+"/status", map[string]string{"status": "active"}, nil)
	v1, v2 := p.Variants[0].ID, p.Variants[1].ID

	var list struct{ ID string }
	s.must(201, "POST", "/v1/admin/pricing/price-lists", map[string]any{"code": "public", "name": "Público", "is_default": true}, &list)
	for _, v := range []string{v1, v2} {
		s.must(200, "PUT", "/v1/admin/pricing/price-lists/"+list.ID+"/variants/"+v, map[string]any{"prices": []map[string]any{
			{"currency": "PEN", "min_qty": 1, "unit_price": "100.00"},
		}}, nil)
	}
	s.must(200, "POST", "/v1/admin/inventory/"+v1+"/adjust", map[string]any{"delta": 5, "reason": "purchase"}, nil)

	var reg sessionResp
	s.do("POST", "/v1/auth/register", false, map[string]string{"email": "ana@clinica.pe", "password": "brackets de acero 2026"}, &reg)
	cust := s.as(reg.Token)
	cust.must(200, "PUT", "/v1/me/customer", map[string]string{"type": "empresa", "document_type": "RUC", "document_number": "20131312955", "business_name": "Clínica Ana SAC"}, nil)
	var addr struct{ ID string }
	cust.must(201, "POST", "/v1/me/addresses", map[string]string{"recipient": "Ana", "line1": "Av. Larco 123", "district": "Miraflores", "province": "Lima", "department": "15"}, &addr)
	return shop{s: s, v1: v1, v2: v2, customer: cust, accountID: reg.Account.ID, addressID: addr.ID}
}

func (sh shop) available(t *testing.T, v string) int {
	t.Helper()
	var a []struct{ Available int }
	sh.s.do("GET", "/v1/inventory/availability?variant_id="+v, false, nil, &a)
	return a[0].Available
}

func (sh shop) fillCart(t *testing.T, qty int) {
	t.Helper()
	sh.customer.must(200, "POST", "/v1/cart/items", map[string]any{"variant_id": sh.v1, "qty": qty}, nil)
}

func (sh shop) place(t *testing.T, method string) orderResp {
	t.Helper()
	var o orderResp
	sh.customer.must(201, "POST", "/v1/orders", map[string]any{"currency": "PEN", "address_id": sh.addressID, "payment_method": method}, &o)
	return o
}

func TestCheckoutAndCulqiPayment(t *testing.T) {
	sh := newShop(t)
	s := sh.s

	// Anonymous cart, then merged at sign-in.
	var cart cartResp
	if got := s.do("POST", "/v1/cart/items", false, map[string]any{"variant_id": sh.v1, "qty": 2}, &cart); got != 200 || cart.Key == "" {
		t.Fatalf("anonymous add: %d %+v", got, cart)
	}
	anon := s.with("X-Cart-Key", cart.Key)
	anon.must(200, "POST", "/v1/cart/items", map[string]any{"variant_id": sh.v2, "qty": 1}, &cart)
	if cart.Ready || len(cart.Lines) != 2 || cart.Lines[1].Problem != "insufficient_stock" || *cart.Lines[0].UnitPrice != "100.00" || cart.Lines[0].Options != "Slot .018" {
		t.Fatalf("anonymous cart: %+v", cart)
	}
	var merged cartResp
	sh.customer.must(200, "POST", "/v1/cart/merge", map[string]string{"key": cart.Key}, &merged)
	if len(merged.Lines) != 2 || merged.Key != "" {
		t.Fatalf("merged cart: %+v", merged)
	}
	if got := anon.do("GET", "/v1/cart", false, nil, &cart); got != 200 || len(cart.Lines) != 0 {
		t.Fatalf("the anonymous cart is gone after merging: %+v", cart)
	}

	// The sold-out line blocks checkout.
	var problem httpserver.Problem
	if got := sh.customer.do("POST", "/v1/orders", false, map[string]any{"address_id": sh.addressID, "payment_method": "culqi"}, &problem); got != 409 || problem.Code != "orders.item_insufficient_stock" {
		t.Fatalf("sold out: %d %+v", got, problem)
	}
	sh.customer.must(200, "DELETE", "/v1/cart/items/"+sh.v2, nil, nil)

	// Quote: 2 × 100 + delivery to Lima (1 kg: S/ 10) + 18 % IGV.
	var quote struct {
		ShippingAmount string `json:"shipping_amount"`
		Tax, Total     string
		Shipping       struct{ Zone string }
	}
	sh.customer.must(200, "POST", "/v1/checkout/quote", map[string]any{"currency": "PEN", "address_id": sh.addressID}, &quote)
	if quote.ShippingAmount != "10.00" || quote.Tax != "37.80" || quote.Total != "247.80" || quote.Shipping.Zone != "lima" {
		t.Fatalf("quote: %+v", quote)
	}

	// Place the order twice with the same Idempotency-Key: one order.
	idem := sh.customer.with("Idempotency-Key", "checkout-1")
	var o, again orderResp
	idem.must(201, "POST", "/v1/orders", map[string]any{"currency": "PEN", "address_id": sh.addressID, "payment_method": "culqi"}, &o)
	idem.must(201, "POST", "/v1/orders", map[string]any{"currency": "PEN", "address_id": sh.addressID, "payment_method": "culqi"}, &again)
	if o.Number == "" || again.Number != o.Number || o.Total != "247.80" || o.Status != "pending_payment" || o.Billing.InvoiceType != "01" || o.Lines[0].Name != "Kit Roth" {
		t.Fatalf("order: %+v / %+v", o, again)
	}
	if got := idem.do("POST", "/v1/orders", false, map[string]any{"currency": "PEN", "address_id": sh.addressID, "payment_method": "bank_transfer"}, &problem); got != 409 || problem.Code != "orders.idempotency_mismatch" {
		t.Fatalf("reused key with another body: %d %+v", got, problem)
	}
	if got := sh.customer.do("POST", "/v1/checkout/quote", false, map[string]any{"currency": "USD", "address_id": sh.addressID}, &problem); got != 422 || problem.Code != "orders.currency_invalid" {
		t.Fatalf("only soles: %d %+v", got, problem)
	}
	var emptied cartResp
	sh.customer.must(200, "GET", "/v1/cart", nil, &emptied)
	if len(emptied.Lines) != 0 {
		t.Fatal("the cart is emptied by the order")
	}
	if sh.available(t, sh.v1) != 3 {
		t.Fatal("2 units reserved")
	}

	// A declined card leaves the order payable.
	if got := sh.customer.do("POST", "/v1/orders/"+o.Number+"/pay", false, map[string]string{"token": "tkn_test_declined"}, &problem); got != 422 || problem.Code != "orders.payment_declined" || problem.Detail == "" {
		t.Fatalf("declined: %d %+v", got, problem)
	}
	var paid orderResp
	sh.customer.must(200, "POST", "/v1/orders/"+o.Number+"/pay", map[string]string{"token": "tkn_test_ok"}, &paid)
	if paid.Status != "paid" {
		t.Fatalf("paid: %+v", paid)
	}
	if got := sh.customer.do("POST", "/v1/orders/"+o.Number+"/pay", false, map[string]string{"token": "tkn_test_ok"}, &problem); got != 409 || problem.Code != "orders.not_payable" {
		t.Fatalf("paying twice: %d %+v", got, problem)
	}
	var stock []struct {
		OnHand   int `json:"on_hand"`
		Reserved int `json:"reserved"`
	}
	s.do("GET", "/v1/admin/inventory?variant_id="+sh.v1, true, nil, &stock)
	if stock[0].OnHand != 3 || stock[0].Reserved != 0 {
		t.Fatalf("payment turns the reservation into a sale: %+v", stock)
	}

	// Another customer cannot see it.
	var other sessionResp
	s.do("POST", "/v1/auth/register", false, map[string]string{"email": "luis@clinica.pe", "password": "brackets de acero 2026"}, &other)
	if got := s.as(other.Token).do("GET", "/v1/orders/"+o.Number, false, nil, nil); got != 404 {
		t.Fatalf("someone else's order: %d", got)
	}

	// Fulfilment.
	var detail struct {
		Payments []struct{ Status string }
		History  []struct{ To string }
	}
	s.must(200, "POST", "/v1/admin/orders/"+o.ID+"/prepare", nil, nil)
	if got := s.do("POST", "/v1/admin/orders/"+o.ID+"/ship", true, map[string]string{}, &problem); got != 422 || problem.Code != "orders.carrier_required" {
		t.Fatalf("ship without carrier: %d %+v", got, problem)
	}
	s.must(200, "POST", "/v1/admin/orders/"+o.ID+"/ship", map[string]string{"carrier": "Olva Courier", "tracking": "OLV-1"}, nil)
	s.must(200, "POST", "/v1/admin/orders/"+o.ID+"/deliver", nil, nil)
	s.must(200, "GET", "/v1/admin/orders/"+o.ID, nil, &detail)
	if len(detail.Payments) != 2 || detail.Payments[0].Status != "failed" || detail.Payments[1].Status != "confirmed" || len(detail.History) != 5 || detail.History[4].To != "delivered" {
		t.Fatalf("detail: %+v", detail)
	}
}

func TestLostChargeResponseIsReconciledByWebhook(t *testing.T) {
	sh := newShop(t)
	sh.fillCart(t, 1)
	o := sh.place(t, "culqi")

	var problem httpserver.Problem
	if got := sh.customer.do("POST", "/v1/orders/"+o.Number+"/pay", false, map[string]string{"token": "tkn_test_timeout"}, &problem); got != 409 || problem.Code != "orders.payment_unknown" {
		t.Fatalf("lost response: %d %+v", got, problem)
	}
	var mid orderResp
	sh.customer.must(200, "GET", "/v1/orders/"+o.Number, nil, &mid)
	if mid.Status != "pending_payment" || !mid.PaymentPending {
		t.Fatalf("while unknown: %+v", mid)
	}
	if got := sh.customer.do("POST", "/v1/orders/"+o.Number+"/pay", false, map[string]string{"token": "tkn_test_ok"}, &problem); got != 409 || problem.Code != "orders.payment_in_progress" {
		t.Fatalf("retry during the unknown window must not charge again: %d %+v", got, problem)
	}

	charges := sh.s.gateway.Charges()
	body := map[string]any{"object": "event", "type": "charge.creation.succeeded", "data": map[string]string{"object": "charge", "id": charges[0].ID}}
	if got := sh.s.do("POST", "/v1/webhooks/culqi/wrong-secret", false, body, nil); got != http.StatusNotFound {
		t.Fatalf("wrong webhook secret: %d", got)
	}
	for range 2 { // Culqi may deliver twice
		if got := sh.s.do("POST", "/v1/webhooks/culqi/"+webhookSecret, false, body, nil); got != 200 {
			t.Fatalf("webhook: %d", got)
		}
	}
	var after orderResp
	sh.customer.must(200, "GET", "/v1/orders/"+o.Number, nil, &after)
	if after.Status != "paid" || after.PaymentPending {
		t.Fatalf("reconciled: %+v", after)
	}
	var n int
	_ = sh.s.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM payments WHERE status = 'confirmed'").Scan(&n)
	if n != 1 {
		t.Fatalf("one confirmed payment, got %d", n)
	}
	unknown := map[string]any{"object": "event", "type": "order.status.changed", "data": map[string]string{"object": "order", "id": "ord_1"}}
	if got := sh.s.do("POST", "/v1/webhooks/culqi/"+webhookSecret, false, unknown, nil); got != 200 {
		t.Fatalf("other events are acknowledged: %d", got)
	}
}

func TestBankTransferCancelConfirmAndExpiry(t *testing.T) {
	sh := newShop(t)
	s := sh.s

	sh.fillCart(t, 2)
	first := sh.place(t, "bank_transfer")
	if sh.available(t, sh.v1) != 3 {
		t.Fatal("transfer orders reserve stock too")
	}
	var problem httpserver.Problem
	if got := sh.customer.do("POST", "/v1/orders/"+first.Number+"/pay", false, map[string]string{"token": "tkn_test_ok"}, &problem); got != 409 || problem.Code != "orders.not_payable" {
		t.Fatalf("transfer orders are not charged by card: %d %+v", got, problem)
	}
	sh.customer.must(200, "POST", "/v1/orders/"+first.Number+"/cancel", nil, nil)
	if sh.available(t, sh.v1) != 5 {
		t.Fatal("cancelling releases the stock")
	}

	sh.fillCart(t, 1)
	second := sh.place(t, "bank_transfer")
	if got := s.do("POST", "/v1/admin/orders/"+second.ID+"/confirm-transfer", true, map[string]string{}, &problem); got != 422 || problem.Code != "orders.reference_required" {
		t.Fatalf("reference required: %d", got)
	}
	var confirmed orderResp
	s.must(200, "POST", "/v1/admin/orders/"+second.ID+"/confirm-transfer", map[string]string{"reference": "OP-778899"}, &confirmed)
	if confirmed.Status != "paid" {
		t.Fatalf("confirmed: %+v", confirmed)
	}
	if got := sh.customer.do("POST", "/v1/orders/"+second.Number+"/cancel", false, nil, &problem); got != 409 || problem.Code != "orders.cannot_cancel_paid" {
		t.Fatalf("paid orders need a refund: %d %+v", got, problem)
	}

	sh.fillCart(t, 1)
	third := sh.place(t, "bank_transfer")
	if _, err := s.db.ExecContext(context.Background(), "UPDATE orders SET payment_deadline = NOW(6) - INTERVAL 1 MINUTE WHERE number = ?", third.Number); err != nil {
		t.Fatal(err)
	}
	n, err := s.app.Orders.ExpireOverdue(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("expire: %d %v", n, err)
	}
	var expired orderResp
	sh.customer.must(200, "GET", "/v1/orders/"+third.Number, nil, &expired)
	if expired.Status != "cancelled" || sh.available(t, sh.v1) != 4 {
		t.Fatalf("expired order: %+v, available %d", expired, sh.available(t, sh.v1))
	}

	events := outboxEvents(t, s)
	want := map[string]int{"OrderPlaced": 3, "OrderCancelled": 2, "PaymentConfirmed": 1}
	got := map[string]int{}
	for _, e := range events {
		got[e]++
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("outbox %s: got %d, want %d (all: %v)", k, got[k], v, events)
		}
	}

	var mine struct{ Total int }
	sh.customer.must(200, "GET", "/v1/orders", nil, &mine)
	if mine.Total != 3 {
		t.Fatalf("my orders: %d", mine.Total)
	}
}

func TestCheckoutNeedsBillingAndAddress(t *testing.T) {
	s := newServer(t)
	var reg sessionResp
	s.do("POST", "/v1/auth/register", false, map[string]string{"email": "nuevo@clinica.pe", "password": "brackets de acero 2026"}, &reg)
	var problem httpserver.Problem
	if got := s.as(reg.Token).do("POST", "/v1/orders", false, map[string]any{"pickup": true, "payment_method": "culqi"}, &problem); got != 409 || problem.Code != "orders.billing_missing" {
		t.Fatalf("no billing: %d %+v", got, problem)
	}
	if got := s.do("POST", "/v1/orders", false, map[string]any{"pickup": true, "payment_method": "culqi"}, nil); got != 401 {
		t.Fatalf("anonymous checkout: %d", got)
	}
}

func outboxEvents(t *testing.T, s server) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), "SELECT event_type FROM outbox ORDER BY occurred_at, id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var events []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}
