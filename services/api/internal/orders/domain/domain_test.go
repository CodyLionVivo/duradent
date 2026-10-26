package domain

import (
	"testing"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

var now = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func code(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

func pen(m int64) money.Money { return money.New(m, money.PEN) }

func newOrder(t *testing.T, method PaymentMethod, unit int64, qty int) *Order {
	t.Helper()
	o, err := NewOrder(NewOrderInput{
		Number: "DD-000001", AccountID: id.New(), Email: "a@b.pe", Method: method, Currency: money.PEN,
		Lines:    []Line{{VariantID: id.New(), SKU: "X", Name: "Kit", UnitPrice: pen(unit), Qty: qty}},
		Shipping: Shipping{Zone: "lima"}, ShipCost: pen(1000),
		Billing: Billing{DocumentType: "RUC", DocumentNumber: "20131312955", InvoiceType: "01"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestCartAddSetMerge(t *testing.T) {
	a, b := id.New(), id.New()
	c := NewCart(id.Nil, "k", now)
	_ = c.Add(a, 2, now)
	_ = c.Add(a, 3, now)
	if c.Items[0].Qty != 5 {
		t.Fatalf("add accumulates: %+v", c.Items)
	}
	if code(c.Set(a, 10000, now)) != "orders.qty_invalid" {
		t.Fatal("max 9999 per line")
	}
	_ = c.Set(a, 0, now)
	if !c.Empty() {
		t.Fatal("zero removes the line")
	}

	mine := NewCart(id.New(), "", now)
	_ = mine.Add(a, 1, now)
	anon := NewCart(id.Nil, "k", now)
	_ = anon.Add(a, 2, now)
	_ = anon.Add(b, 1, now)
	mine.Merge(anon, now)
	if len(mine.Items) != 2 || mine.Items[0].Qty != 3 {
		t.Fatalf("merge: %+v", mine.Items)
	}
}

func TestTotalsAddIGVOnGoodsAndDelivery(t *testing.T) {
	tax, total := Totals(pen(10000), pen(1000)) // S/ 100 + S/ 10
	if tax.Minor != 1980 || total.Minor != 12980 {
		t.Fatalf("tax %v total %v", tax, total)
	}
	tax, _ = Totals(pen(1), pen(2)) // 0.03 * 18% = 0.0054 → 0.01 (half up)
	if tax.Minor != 1 {
		t.Fatalf("rounding: %v", tax)
	}
}

func TestNewOrderSnapshotsTotals(t *testing.T) {
	o := newOrder(t, MethodCulqi, 8500, 2)
	if o.Subtotal.Minor != 17000 || o.Tax.Minor != 3240 || o.Total.Minor != 21240 || o.Lines[0].LineTotal.Minor != 17000 {
		t.Fatalf("totals: %+v", o)
	}
	if o.Status != StatusPendingPayment || !o.PaymentDeadline.Equal(now.Add(CulqiPaymentWindow)) {
		t.Fatalf("status/deadline: %s %v", o.Status, o.PaymentDeadline)
	}
	tr := newOrder(t, MethodBankTransfer, 100, 1)
	if !tr.PaymentDeadline.Equal(now.Add(TransferPaymentWindow)) {
		t.Fatal("bank transfers get 48 hours")
	}
	_, err := NewOrder(NewOrderInput{Method: MethodCulqi, Currency: money.PEN, Lines: o.Lines, ShipCost: pen(0)}, now)
	if code(err) != "orders.billing_missing" {
		t.Fatalf("billing required: %v", err)
	}
}

func TestChargeGuard(t *testing.T) {
	o := newOrder(t, MethodCulqi, 8500, 1)
	if err := o.BeginCharge("tkn_test_1", now); err != nil {
		t.Fatal(err)
	}
	if code(o.BeginCharge("tkn_test_2", now.Add(time.Second))) != "orders.payment_in_progress" {
		t.Fatal("a second charge while the first is in flight must be refused")
	}
	if code(o.Cancel("", "customer", now.Add(time.Second))) != "orders.payment_in_progress" {
		t.Fatal("cannot cancel while charging")
	}
	o.EndCharge(now)
	if err := o.BeginCharge("tkn_test_3", now.Add(2*time.Second)); err != nil {
		t.Fatalf("after a failed charge the customer can retry: %v", err)
	}

	late := newOrder(t, MethodCulqi, 8500, 1)
	if code(late.BeginCharge("tkn_x", now.Add(CulqiPaymentWindow))) != "orders.payment_expired" {
		t.Fatal("payment window")
	}
	transfer := newOrder(t, MethodBankTransfer, 8500, 1)
	if code(transfer.BeginCharge("tkn_x", now)) != "orders.not_payable" {
		t.Fatal("bank transfer orders are not charged by card")
	}
}

func TestYapeLimit(t *testing.T) {
	small := newOrder(t, MethodCulqi, 100_00, 1)
	if err := small.BeginCharge("ype_test_1", now); err != nil {
		t.Fatal(err)
	}
	big := newOrder(t, MethodCulqi, 2000_00, 1) // plus delivery and IGV: over S/ 2,000
	if code(big.BeginCharge("ype_test_1", now)) != "orders.yape_limit" {
		t.Fatal("Yape is capped at S/ 2,000")
	}
	if err := big.BeginCharge("tkn_test_1", now); err != nil {
		t.Fatal("cards have no such cap")
	}
}

func TestLifecycle(t *testing.T) {
	o := newOrder(t, MethodCulqi, 8500, 1)
	if code(o.Ship("Olva", "123", "admin", now)) != "orders.invalid_transition" {
		t.Fatal("cannot ship before payment")
	}
	if err := o.MarkPaid("culqi", "chr_1", now); err != nil {
		t.Fatal(err)
	}
	if err := o.MarkPaid("culqi", "chr_1", now); err != nil {
		t.Fatal("marking paid twice is idempotent")
	}
	if code(o.Cancel("", "customer", now)) != "orders.cannot_cancel_paid" {
		t.Fatal("paid orders are not cancelled without refund")
	}
	if err := o.StartPreparing("admin", now); err != nil {
		t.Fatal(err)
	}
	if code(o.Ship("", "", "admin", now)) != "orders.carrier_required" {
		t.Fatal("carrier required for delivery")
	}
	if err := o.Ship("Olva Courier", "OLV123", "admin", now); err != nil {
		t.Fatal(err)
	}
	if err := o.Deliver("admin", now); err != nil {
		t.Fatal(err)
	}
	if len(o.History) != 5 || o.History[4].To != StatusDelivered {
		t.Fatalf("history: %+v", o.History)
	}
}

func TestOverdue(t *testing.T) {
	o := newOrder(t, MethodBankTransfer, 100, 1)
	if o.Overdue(now.Add(47 * time.Hour)) {
		t.Fatal("not yet")
	}
	if !o.Overdue(now.Add(48 * time.Hour)) {
		t.Fatal("48 hours passed")
	}
	_ = o.MarkPaid("admin", "", now)
	if o.Overdue(now.Add(72 * time.Hour)) {
		t.Fatal("paid orders are never overdue")
	}
}

func TestRefund(t *testing.T) {
	now := time.Now()
	o := newOrder(t, MethodCulqi, 10000, 1)
	if code(o.CanRefund(false)) != "orders.not_refundable" {
		t.Fatal("unpaid orders have nothing to refund")
	}
	_ = o.MarkPaid("culqi", "chr_1", now)
	if err := o.Refund("cliente desistió", "ref_1", "admin", now); err != nil {
		t.Fatal(err)
	}
	if o.Status != StatusCancelled || o.RefundRef != "ref_1" || o.RefundedAt == nil || o.CancelReason != "cliente desistió" {
		t.Fatalf("%+v", o)
	}
	if code(o.CanRefund(true)) != "orders.already_refunded" {
		t.Fatal("refunds happen once")
	}

	shipped := newOrder(t, MethodCulqi, 10000, 1)
	_ = shipped.MarkPaid("culqi", "chr_2", now)
	_ = shipped.Ship("Olva", "OLV-1", "admin", now)
	if code(shipped.CanRefund(true)) != "orders.not_refundable" {
		t.Fatal("shipped orders are returned, not refunded here")
	}

	// A payment that arrived after the order expired is refundable.
	late := newOrder(t, MethodCulqi, 10000, 1)
	_ = late.Cancel("payment window expired", "system", now)
	if err := late.Refund("pago tardío", "ref_3", "admin", now); err != nil || late.RefundedAt == nil {
		t.Fatal(err)
	}
}
