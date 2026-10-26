package apitest

import (
	"context"
	"slices"
	"strings"
	"testing"

	invoicingapp "github.com/CodyLionVivo/duradent/services/api/internal/invoicing/application"
	invoicingports "github.com/CodyLionVivo/duradent/services/api/internal/invoicing/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	notifapp "github.com/CodyLionVivo/duradent/services/api/internal/notifications/application"
	notifports "github.com/CodyLionVivo/duradent/services/api/internal/notifications/ports"
)

type ev struct {
	id, agg id.ID
	typ     string
	payload []byte
}

func unpublished(t *testing.T, s server) []ev {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), "SELECT id, event_type, aggregate_id, payload FROM outbox WHERE published_at IS NULL ORDER BY occurred_at, id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var evs []ev
	for rows.Next() {
		var e ev
		if err := rows.Scan(&e.id, &e.typ, &e.agg, &e.payload); err != nil {
			t.Fatal(err)
		}
		evs = append(evs, e)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return evs
}

// drain plays the relay and the worker: it hands every unpublished outbox
// event to the consumers subscribed to its type, until no new events appear
// (invoicing emits InvoiceIssued, which notifications then handles).
func drain(t *testing.T, s server) {
	t.Helper()
	ctx := context.Background()
	for range 5 {
		evs := unpublished(t, s)
		if len(evs) == 0 {
			return
		}
		for _, e := range evs {
			if _, err := s.db.ExecContext(ctx, "UPDATE outbox SET published_at = NOW(6) WHERE id = ?", e.id); err != nil {
				t.Fatal(err)
			}
			if slices.Contains(notifapp.Events, e.typ) {
				if err := s.app.Notify.Handle(ctx, notifports.Event{ID: e.id, Type: e.typ, AggregateID: e.agg, Payload: e.payload}); err != nil {
					t.Fatalf("notifications %s: %v", e.typ, err)
				}
			}
			if slices.Contains(invoicingapp.Events, e.typ) {
				if err := s.app.Invoicing.Handle(ctx, invoicingports.Event{ID: e.id, Type: e.typ, AggregateID: e.agg, Payload: e.payload}); err != nil {
					t.Fatalf("invoicing %s: %v", e.typ, err)
				}
			}
		}
	}
	t.Fatal("events keep coming")
}

func subjects(s server) []string {
	var out []string
	for _, m := range s.mail.all() {
		out = append(out, m.To+": "+m.Subject)
	}
	return out
}

type invoiceResp struct {
	ID, Name, Type, Status, Series string
	Number                         int
	PDFURL                         string `json:"pdf_url"`
	LastError                      string `json:"last_error"`
}

func TestEmailVerificationEndToEnd(t *testing.T) {
	sh := newShop(t)
	s := sh.s
	var link string
	for _, m := range s.mail.all() {
		if m.Subject == "Confirma tu correo" {
			_, link, _ = strings.Cut(m.Text, "token=")
			link = strings.TrimSpace(link)
		}
	}
	if link == "" {
		t.Fatalf("no verification email: %v", subjects(s))
	}
	var me struct {
		Account struct {
			EmailVerified bool `json:"email_verified"`
		}
	}
	sh.customer.must(200, "GET", "/v1/me", nil, &me)
	if me.Account.EmailVerified {
		t.Fatal("password accounts start unverified")
	}
	s.must(204, "POST", "/v1/auth/verify-email", map[string]string{"token": link}, nil)
	sh.customer.must(200, "GET", "/v1/me", nil, &me)
	if !me.Account.EmailVerified {
		t.Fatal("the link confirms the email")
	}
	if got := s.do("POST", "/v1/auth/verify-email", false, map[string]string{"token": link}, nil); got != 422 {
		t.Fatalf("a link works once: %d", got)
	}
	if got := sh.customer.do("POST", "/v1/me/verify-email", false, nil, nil); got != 409 {
		t.Fatalf("resend after verifying: %d", got)
	}

	drain(t, s)
	if !slices.Contains(subjects(s), "ana@clinica.pe: Bienvenido a Duradent") {
		t.Fatalf("welcome email: %v", subjects(s))
	}
}

func TestPaidOrderGetsFacturaAndRefundGetsCreditNote(t *testing.T) {
	sh := newShop(t)
	s := sh.s
	sh.fillCart(t, 2)
	o := sh.place(t, "culqi")
	sh.customer.must(200, "POST", "/v1/orders/"+o.Number+"/pay", map[string]string{"token": "tkn_test_ok"}, nil)
	if got := sh.available(t, sh.v1); got != 3 {
		t.Fatalf("available after the sale: %d", got)
	}
	drain(t, s)

	var invs struct{ Items []invoiceResp }
	sh.customer.must(200, "GET", "/v1/orders/"+o.Number+"/invoices", nil, &invs)
	if len(invs.Items) != 1 || invs.Items[0].Name != "Factura F001-1" || invs.Items[0].Status != "accepted" || invs.Items[0].PDFURL == "" {
		t.Fatalf("factura: %+v", invs.Items)
	}
	doc := s.ose.Issued()["F001-1"]
	if doc.Customer.DocNumber != "20131312955" || doc.Customer.Address != "Av. Larco 123, Miraflores, Lima, 15" || doc.Total != 24780 {
		t.Fatalf("document sent to the OSE: %+v", doc)
	}
	got := subjects(s)
	for _, want := range []string{
		"ana@clinica.pe: Recibimos tu pedido " + o.Number,
		"ana@clinica.pe: Pago confirmado · pedido " + o.Number,
		"ana@clinica.pe: Factura F001-1 · pedido " + o.Number,
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q in %v", want, got)
		}
	}
	// Another customer cannot see the invoices.
	var other sessionResp
	s.do("POST", "/v1/auth/register", false, map[string]string{"email": "otro@clinica.pe", "password": "brackets de acero 2026"}, &other)
	if code := s.as(other.Token).do("GET", "/v1/orders/"+o.Number+"/invoices", false, nil, nil); code != 404 {
		t.Fatalf("someone else's invoices: %d", code)
	}

	// Refund: Culqi refund, stock back on hand, credit note, email.
	if code := s.do("POST", "/v1/admin/orders/"+o.ID+"/refund", true, map[string]string{}, nil); code != 422 {
		t.Fatalf("refund without a reason: %d", code)
	}
	var refunded struct {
		Status     string
		RefundRef  string  `json:"refund_ref"`
		RefundedAt *string `json:"refunded_at"`
	}
	s.must(200, "POST", "/v1/admin/orders/"+o.ID+"/refund", map[string]string{"reason": "Producto equivocado"}, &refunded)
	if refunded.Status != "cancelled" || !strings.HasPrefix(refunded.RefundRef, "ref_test_") || refunded.RefundedAt == nil {
		t.Fatalf("refund: %+v", refunded)
	}
	if code := s.do("POST", "/v1/admin/orders/"+o.ID+"/refund", true, map[string]string{"reason": "otra vez"}, nil); code != 409 {
		t.Fatalf("second refund: %d", code)
	}
	if got := sh.available(t, sh.v1); got != 5 {
		t.Fatalf("available after the refund: %d", got)
	}
	drain(t, s)
	var all struct{ Items []invoiceResp }
	s.must(200, "GET", "/v1/admin/orders/"+o.ID+"/invoices", nil, &all)
	if len(all.Items) != 2 || all.Items[1].Name != "Nota de crédito FC01-1" || all.Items[1].Status != "accepted" {
		t.Fatalf("credit note: %+v", all.Items)
	}
	if nc := s.ose.Issued()["FC01-1"]; nc.RefSeries != "F001" || nc.RefNumber != 1 || nc.Reason != "Producto equivocado" {
		t.Fatalf("credit note sent: %+v", nc)
	}
	if !slices.Contains(subjects(s), "ana@clinica.pe: Reembolso del pedido "+o.Number) {
		t.Fatalf("refund email: %v", subjects(s))
	}
	// Redelivering everything changes nothing.
	before := len(s.mail.all())
	if _, err := s.db.ExecContext(context.Background(), "UPDATE outbox SET published_at = NULL"); err != nil {
		t.Fatal(err)
	}
	drain(t, s)
	if len(s.mail.all()) != before || len(s.ose.Issued()) != 2 {
		t.Fatalf("redelivery sent %d more emails and %d documents", len(s.mail.all())-before, len(s.ose.Issued()))
	}
}

func TestRejectedInvoiceAlertsOperationsAndCanBeRetried(t *testing.T) {
	sh := newShop(t)
	s := sh.s
	sh.customer.must(200, "PUT", "/v1/me/customer", map[string]string{"type": "empresa", "document_type": "RUC", "document_number": "20131312955", "business_name": "Clínica Rechazo SAC"}, nil)
	sh.fillCart(t, 1)
	o := sh.place(t, "bank_transfer")
	s.must(200, "POST", "/v1/admin/orders/"+o.ID+"/confirm-transfer", map[string]string{"reference": "OP-778899"}, nil)
	drain(t, s)

	var list struct {
		Items []invoiceResp
		Total int
	}
	s.must(200, "GET", "/v1/admin/invoices?status=rejected", nil, &list)
	if list.Total != 1 || !strings.Contains(list.Items[0].LastError, "RUC") {
		t.Fatalf("rejected list: %+v", list)
	}
	if !slices.Contains(subjects(s), adminEmail+": Error al emitir comprobante · "+o.Number) {
		t.Fatalf("ops alert: %v", subjects(s))
	}
	var mine struct{ Items []invoiceResp }
	sh.customer.must(200, "GET", "/v1/orders/"+o.Number+"/invoices", nil, &mine)
	if len(mine.Items) != 0 {
		t.Fatal("customers only see accepted documents")
	}
	// Still rejected on retry (same data): the number is kept, not reused.
	var retried invoiceResp
	s.must(200, "POST", "/v1/admin/invoices/"+list.Items[0].ID+"/retry", nil, &retried)
	if retried.Status != "rejected" || retried.Name != "Factura F001-1" {
		t.Fatalf("retry: %+v", retried)
	}
	if code := s.do("POST", "/v1/admin/invoices/"+id.New().String()+"/retry", true, nil, nil); code != 404 {
		t.Fatalf("unknown invoice: %d", code)
	}
}
