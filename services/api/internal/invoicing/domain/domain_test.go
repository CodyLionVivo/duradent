package domain

import (
	"testing"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

func pen(m int64) money.Money { return money.New(m, money.PEN) }

// An order whose per-line IGV rounds to one céntimo more than the order IGV.
func sale() Sale {
	// Three lines of S/ 0.03: each rounds its IGV up to 0.01, so the lines
	// carry two céntimos more than the IGV of the whole order.
	return Sale{
		OrderID: id.New(), Number: "DD-000009",
		Customer: Customer{InvoiceType: "01", DocType: "RUC", DocNumber: "20123456789", Name: "Clínica Sonrisa SAC", Address: "Av. Arequipa 123, Lima"},
		Lines: []SaleLine{
			{SKU: "A", Name: "Bracket", UnitPrice: pen(3), Qty: 1, LineTotal: pen(3)},
			{SKU: "B", Name: "Arco", UnitPrice: pen(3), Qty: 1, LineTotal: pen(3)},
			{SKU: "C", Name: "Ligadura", UnitPrice: pen(3), Qty: 1, LineTotal: pen(3)},
			{SKU: "D", Name: "Resina", UnitPrice: pen(3333), Qty: 3, LineTotal: pen(9999)},
		},
		Shipping: pen(1000),
		// base 9 + 9999 + 1000 = 11008 → IGV 1981.44 → 1981
		Tax: pen(1981), Total: pen(12989),
	}
}

func TestBuildDocumentLinesAddUpToTheOrder(t *testing.T) {
	s := sale()
	inv := NewInvoice(s.OrderID, s.Number, SaleType(s.Customer), "F001", 7, s.Total, nil, time.Now())
	d, err := BuildDocument(inv, s, nil, "", time.Date(2026, 10, 27, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if inv.Type != Factura || len(d.Lines) != 5 || d.Lines[4].Unit != UnitService {
		t.Fatalf("%+v", d.Lines)
	}
	var igv, total int64
	for _, l := range d.Lines {
		igv += l.IGV
		total += l.Total
		if l.Total != l.Subtotal+l.IGV {
			t.Fatalf("line %s", l.Code)
		}
	}
	if igv != 1981 || total != 12989 || d.TaxedTotal != 11008 || d.Lines[3].IGV != 1798 {
		t.Fatalf("igv %d total %d gravada %d", igv, total, d.TaxedTotal)
	}
	if d.IssueDate.Day() != 26 {
		t.Fatal("the issue date is the date in Lima")
	}
	// Resina absorbs the difference: (99.99 + 17.98) / 3 with ten decimals.
	if got := d.Lines[3].UnitPrice(); got != "39.3233333333" {
		t.Fatalf("unit price %s (line %+v)", got, d.Lines[3])
	}
}

func TestBoletaAndCreditNote(t *testing.T) {
	s := sale()
	s.Customer = Customer{InvoiceType: "03", DocType: "DNI", DocNumber: "44556677", Name: "Ana Rojas"}
	series := Series{Factura: "F001", Boleta: "B001", CreditNoteF: "FC01", CreditNoteB: "BC01"}
	if err := series.Validate(); err != nil {
		t.Fatal(err)
	}
	if SaleType(s.Customer) != Boleta {
		t.Fatal("DNI customers get a boleta")
	}
	b := NewInvoice(s.OrderID, s.Number, Boleta, series.For(Boleta, nil), 1, s.Total, nil, time.Now())
	if series.For(CreditNote, b) != "BC01" {
		t.Fatal("credit note of a boleta uses the B series")
	}
	nc := NewInvoice(s.OrderID, s.Number, CreditNote, "BC01", 1, s.Total, b, time.Now())
	d, err := BuildDocument(nc, s, b, "", time.Now())
	if err != nil || d.RefSeries != "B001" || d.RefNumber != 1 || d.Reason == "" {
		t.Fatalf("%+v %v", d, err)
	}
	if nc.Name() != "Nota de crédito BC01-1" {
		t.Fatal(nc.Name())
	}
}

func TestDocumentsAreInSoles(t *testing.T) {
	s := sale()
	s.Total = money.New(s.Total.Minor, "USD")
	inv := NewInvoice(s.OrderID, s.Number, Factura, "F001", 1, s.Total, nil, time.Now())
	if _, err := BuildDocument(inv, s, nil, "", time.Now()); err == nil {
		t.Fatal("only soles can be invoiced")
	}
}

func TestInvoiceLifecycle(t *testing.T) {
	inv := NewInvoice(id.New(), "DD-1", Factura, "F001", 1, pen(100), nil, time.Now())
	if err := inv.Retry(time.Now()); err == nil {
		t.Fatal("pending documents are not retried by hand")
	}
	inv.Reject("RUC no existe", time.Now())
	if err := inv.Retry(time.Now()); err != nil || inv.Status != StatusPending {
		t.Fatal(err)
	}
	for range 10 {
		inv.Failed("timeout", time.Now())
	}
	if !inv.GivenUp() {
		t.Fatal("retries stop after 10 attempts")
	}
	if (Series{Factura: "B001"}).Validate() == nil {
		t.Fatal("factura series start with F")
	}
}
