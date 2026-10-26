package application

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/notifications/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/notifications/ports"
)

type fakeOrders map[id.ID]ports.OrderInfo

func (f fakeOrders) Order(_ context.Context, oid id.ID) (ports.OrderInfo, error) { return f[oid], nil }

type fakeAccounts map[id.ID]ports.Recipient

func (f fakeAccounts) Recipient(_ context.Context, aid id.ID) (ports.Recipient, error) {
	return f[aid], nil
}

type fakeMailer struct {
	sent []domain.Message
	fail bool
}

func (m *fakeMailer) Send(_ context.Context, msg domain.Message) (string, error) {
	if m.fail {
		return "", errors.New("smtp down")
	}
	m.sent = append(m.sent, msg)
	return "msg-1", nil
}

type fakeLog map[string]string

func key(e ports.LogEntry) string { return e.EventID.String() + string(e.Kind) + e.To }

func (l fakeLog) Begin(_ context.Context, e ports.LogEntry, _ time.Time) (bool, error) {
	if l[key(e)] == "sent" {
		return true, nil
	}
	l[key(e)] = "sending"
	return false, nil
}
func (l fakeLog) Sent(_ context.Context, e ports.LogEntry, _ string, _ time.Time) error {
	l[key(e)] = "sent"
	return nil
}
func (l fakeLog) Failed(_ context.Context, e ports.LogEntry, _ string) error {
	l[key(e)] = "failed"
	return nil
}

func setup() (*Service, *fakeMailer, id.ID) {
	acc, oid := id.New(), id.New()
	mailer := &fakeMailer{}
	svc := NewService(Deps{
		Orders: fakeOrders{oid: {Number: "DD-000042", AccountID: acc, Email: "clinica@ejemplo.pe",
			Total: money.New(14632, money.PEN), Method: "bank_transfer", PaymentDeadline: time.Date(2026, 10, 28, 17, 0, 0, 0, time.UTC)}},
		Accounts: fakeAccounts{acc: {Email: "old@ejemplo.pe", Name: "Dra. Rojas", Locale: locale.PTBR}},
		Mailer:   mailer, Log: fakeLog{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: time.Now,
	}, Config{WebBaseURL: "https://duradent.pe/", BankDetails: "BCP 191", OpsEmails: []string{"ops@duradent.pe"}})
	return svc, mailer, oid
}

func event(typ string, aggregate id.ID, payload any) ports.Event {
	b, _ := json.Marshal(payload)
	return ports.Event{ID: id.New(), Type: typ, AggregateID: aggregate, Payload: b}
}

func TestOrderPlacedEmailsTheCustomerInTheirLanguageOnce(t *testing.T) {
	svc, mailer, oid := setup()
	e := event("OrderPlaced", oid, map[string]any{"order_id": oid})
	for range 2 { // redelivered
		if err := svc.Handle(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	if len(mailer.sent) != 1 {
		t.Fatalf("sent %d emails", len(mailer.sent))
	}
	m := mailer.sent[0]
	if m.To != "clinica@ejemplo.pe" || m.Subject != "Recebemos seu pedido DD-000042" {
		t.Fatalf("%s / %s", m.To, m.Subject)
	}
	for _, want := range []string{"S/ 146.32", "28/10/2026 12:00 (Lima)", "BCP 191", "https://duradent.pe/pt-br/orders/DD-000042"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, m.Text)
		}
	}
	if !strings.Contains(m.HTML, `src="https://duradent.pe/brand/logo-email.png"`) {
		t.Error("the email header shows the logo")
	}
}

func TestFailedSendIsRetried(t *testing.T) {
	svc, mailer, oid := setup()
	mailer.fail = true
	e := event("PaymentConfirmed", oid, map[string]any{"order_id": oid})
	if err := svc.Handle(context.Background(), e); err == nil {
		t.Fatal("a failed send must return an error so SQS redelivers")
	}
	mailer.fail = false
	if err := svc.Handle(context.Background(), e); err != nil || len(mailer.sent) != 1 {
		t.Fatalf("retry: %v %d", err, len(mailer.sent))
	}
}

func TestStockLowGoesToOps(t *testing.T) {
	svc, mailer, _ := setup()
	if err := svc.Handle(context.Background(), event("StockLow", id.New(), map[string]any{"sku": "BRK-022", "available": 3})); err != nil {
		t.Fatal(err)
	}
	if len(mailer.sent) != 1 || mailer.sent[0].To != "ops@duradent.pe" || !strings.Contains(mailer.sent[0].Text, "BRK-022 tiene 3") {
		t.Fatalf("%+v", mailer.sent)
	}
	if err := svc.Handle(context.Background(), event("SomethingElse", id.New(), nil)); err != nil || len(mailer.sent) != 1 {
		t.Fatal("unknown events are ignored")
	}
}

func TestFormatMoney(t *testing.T) {
	for in, want := range map[int64]string{14632: "S/ 146.32", 123456789: "S/ 1,234,567.89", 5: "S/ 0.05"} {
		if got := domain.FormatMoney(money.New(in, money.PEN)); got != want {
			t.Errorf("%d: %s", in, got)
		}
	}
	if got := domain.FormatMoney(money.New(100000, money.USD)); got != "US$ 1,000.00" {
		t.Error(got)
	}
}
