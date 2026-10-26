package domain

import (
	"testing"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

var provider = Provider{Name: "Duradent S.A.C.", RUC: "20000000000", Address: "Av. Emancipación 271, Lima"}

func valid() Input {
	return Input{
		Kind: "reclamo", Name: "Ana Torres", DocType: "dni", DocNumber: "12345678", Address: "Av. Arequipa 123, Lima",
		Phone: "+51 999 888 777", Email: "Ana@Clinica.pe", ItemType: "producto", OrderNumber: "dd-000012", Amount: "1,250.50",
		ItemDescription: "Brackets metálicos Roth 0.022", Detail: "Llegaron 2 cajas abiertas.", Request: "Cambio de las cajas.",
		Channel: "email", Locale: locale.EN,
	}
}

func code(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

func TestNewNormalizesAValidSheet(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	c, err := New(valid(), id.Nil, provider, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.Consumer.Email != "ana@clinica.pe" || c.Consumer.DocType != "DNI" || c.OrderNumber != "DD-000012" {
		t.Fatalf("normalized: %+v", c)
	}
	if c.Amount == nil || c.Amount.Decimal() != "1250.50" || c.Status != StatusOpen || c.Locale != locale.EN {
		t.Fatalf("amount/status: %+v", c)
	}
	c.Assign(7)
	if c.Code() != "000007-2026" {
		t.Fatalf("code %q", c.Code())
	}
}

func TestNewRejectsInvalidSheets(t *testing.T) {
	cases := map[string]func(*Input){
		"complaints.kind_invalid":              func(i *Input) { i.Kind = "sugerencia" },
		"complaints.name_required":             func(i *Input) { i.Name = " " },
		"complaints.document_invalid":          func(i *Input) { i.DocNumber = "1234" },
		"complaints.address_required":          func(i *Input) { i.Address = "" },
		"complaints.email_invalid":             func(i *Input) { i.Email = "ana@clinica" },
		"complaints.phone_invalid":             func(i *Input) { i.Phone = "llámame" },
		"complaints.item_type_invalid":         func(i *Input) { i.ItemType = "otro" },
		"complaints.amount_invalid":            func(i *Input) { i.Amount = "-5" },
		"complaints.item_description_required": func(i *Input) { i.ItemDescription = "" },
		"complaints.detail_required":           func(i *Input) { i.Detail = "" },
		"complaints.request_required":          func(i *Input) { i.Request = "" },
		"complaints.channel_invalid":           func(i *Input) { i.Channel = "whatsapp" },
	}
	for want, edit := range cases {
		in := valid()
		edit(&in)
		if _, err := New(in, id.Nil, provider, time.Now()); code(err) != want {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestDocuments(t *testing.T) {
	ok := map[string]string{"DNI": "12345678", "RUC": "20123456789", "CE": "001234567", "PAS": "AB12345"}
	for typ, n := range ok {
		if !validDocument(typ, n) {
			t.Errorf("%s %s should be valid", typ, n)
		}
	}
	for _, bad := range [][2]string{{"DNI", "1234567A"}, {"RUC", "2012345678"}, {"CE", "12"}, {"LE", "12345678"}} {
		if validDocument(bad[0], bad[1]) {
			t.Errorf("%v should be invalid", bad)
		}
	}
}

func TestDueDateSkipsWeekends(t *testing.T) {
	// Filed on Friday 23 Oct 2026 at 22:00 in Lima (Saturday 03:00 UTC).
	filed := time.Date(2026, 10, 24, 3, 0, 0, 0, time.UTC)
	due := DueDate(filed).In(lima)
	// 15 business days after Friday 23 Oct is Friday 13 Nov.
	if due.Format("2006-01-02 15:04") != "2026-11-13 23:59" {
		t.Fatalf("due %s", due)
	}
	if YearOf(time.Date(2027, 1, 1, 3, 0, 0, 0, time.UTC)) != 2026 {
		t.Fatal("the year is counted in Lima")
	}
}

func TestAnswerOnce(t *testing.T) {
	now := time.Date(2026, 10, 26, 15, 0, 0, 0, time.UTC)
	c, _ := New(valid(), id.Nil, provider, now)
	if code(c.Answer(" ", "jefe@duradent.pe", now)) != "complaints.response_required" {
		t.Fatal("empty answer")
	}
	if err := c.Answer("Cambiamos las cajas el lunes.", "jefe@duradent.pe", now); err != nil || c.Status != StatusAnswered {
		t.Fatalf("answer: %v %+v", err, c)
	}
	if code(c.Answer("otra", "x", now)) != "complaints.already_answered" {
		t.Fatal("second answer must be rejected")
	}
	if c.Overdue(now.AddDate(1, 0, 0)) {
		t.Fatal("an answered sheet is never overdue")
	}
}
