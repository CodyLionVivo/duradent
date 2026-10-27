package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

func TestEveryCustomerEmailExistsInEveryLanguage(t *testing.T) {
	adminOnly := map[Kind]bool{KindAdminStockLow: true, KindAdminInvoiceError: true, KindAdminComplaintFiled: true}
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

func TestComplaintCopyCarriesTheWholeSheet(t *testing.T) {
	filed := time.Date(2026, 10, 26, 20, 30, 0, 0, time.UTC)
	answered := filed.Add(72 * time.Hour)
	amount := money.New(125050, money.PEN)
	c := Complaint{
		Code: "000003-2026", Kind: "queja", FiledAt: filed, ProviderName: "Duradent S.A.C.", ProviderRUC: "20000000000",
		ProviderAddress: "Av. Emancipación 271", Name: "Ana <b>", DocType: "DNI", DocNumber: "12345678", Address: "Lima",
		Email: "ana@clinica.pe", ItemType: "producto", OrderNumber: "DD-000012", Amount: &amount, ItemDescription: "Brackets",
		Detail: "Primera línea\nSegunda línea", Request: "Cambio", Channel: "email", Response: "Hecho.", AnsweredAt: &answered,
	}
	m, err := Render(KindComplaintAnswered, locale.ES, c.Email, Data{
		Name: c.Name, Code: c.Code, ComplaintKind: ComplaintKind(c.Kind, locale.ES), Response: "Cambiamos las cajas.\nGracias.",
		Sheet: ComplaintRows(c, locale.ES),
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "Respuesta a tu hoja de reclamación N.° 000003-2026" || !strings.Contains(m.HTML, "Respuesta a tu queja") {
		t.Fatalf("subject/title: %q", m.Subject)
	}
	for _, want := range []string{"Cambiamos las cajas.<br>Gracias.", "Ana &lt;b&gt;", "S/ 1,250.50", "RUC 20000000000", "4. Observaciones y acciones adoptadas por el proveedor", "Queja: disconformidad"} {
		if !strings.Contains(m.HTML, want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	if !strings.Contains(m.Text, "Detalle: Primera línea\nSegunda línea") || !strings.Contains(m.Text, "Fecha: 26/10/2026 15:30 (Lima)") {
		t.Fatalf("text: %s", m.Text)
	}
	if rows := ComplaintRows(Complaint{Kind: "reclamo"}, locale.PTBR); rows[len(rows)-4].Value != "Reclamação (reclamo): desacordo relacionado aos produtos ou serviços" {
		t.Fatalf("pt-br rows: %+v", rows)
	}
}

func TestEnglishShippedEmail(t *testing.T) {
	m, _ := Render(KindOrderShipped, locale.EN, "a@b.pe", Data{OrderNumber: "DD-1", Carrier: "Olva", Tracking: "OLV-9"})
	if !strings.Contains(m.Text, "Carrier: Olva. Tracking: OLV-9.") {
		t.Fatalf("text: %s", m.Text)
	}
}
