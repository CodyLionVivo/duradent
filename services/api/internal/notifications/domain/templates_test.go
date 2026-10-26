package domain

import (
	"strings"
	"testing"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

func TestEveryCustomerEmailExistsInEveryLanguage(t *testing.T) {
	adminOnly := map[Kind]bool{KindAdminStockLow: true, KindAdminInvoiceError: true}
	for kind, byLocale := range texts {
		if adminOnly[kind] {
			continue
		}
		for _, l := range locale.All {
			if _, ok := byLocale[l]; !ok {
				t.Errorf("%s has no %s version", kind, l)
			}
		}
	}
}

func TestRenderEscapesAndFillsData(t *testing.T) {
	m, err := Render(KindOrderPlaced, locale.ES, "a@b.pe", Data{
		Name: "Ana <script>", OrderNumber: "DD-000007", Total: "S/ 146.32", Method: "bank_transfer",
		BankDetails: "BCP 191-123", Deadline: "28 oct. 2026", Link: "https://duradent.pe/es/orders/DD-000007",
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Recibimos tu pedido DD-000007" {
		t.Fatalf("subject: %q", m.Subject)
	}
	if strings.Contains(m.HTML, "<script>") || !strings.Contains(m.HTML, "Ana &lt;script&gt;") {
		t.Fatal("customer data must be escaped")
	}
	if !strings.Contains(m.HTML, "BCP 191-123") || !strings.Contains(m.Text, "Transfiere el total") || !strings.Contains(m.Text, "https://duradent.pe/es/orders/DD-000007") {
		t.Fatalf("bank transfer instructions and link: %s", m.Text)
	}
}

func TestRenderFallsBackToSpanish(t *testing.T) {
	m, err := Render(KindAdminStockLow, locale.EN, "ops@duradent.pe", Data{SKU: "BRK-1", Available: 3})
	if err != nil || m.Subject != "Stock bajo: BRK-1" {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := Render("nope", locale.ES, "", Data{}); err == nil {
		t.Fatal("unknown kind")
	}
}

func TestEnglishShippedEmail(t *testing.T) {
	m, _ := Render(KindOrderShipped, locale.EN, "a@b.pe", Data{OrderNumber: "DD-1", Carrier: "Olva", Tracking: "OLV-9"})
	if !strings.Contains(m.Text, "Carrier: Olva. Tracking: OLV-9.") {
		t.Fatalf("text: %s", m.Text)
	}
}
