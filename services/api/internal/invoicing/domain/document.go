package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

// Customer is the billing snapshot of the order.
type Customer struct {
	InvoiceType string // 01 factura, 03 boleta (what the customer asked for)
	DocType     string // RUC, DNI, CE
	DocNumber   string
	Name        string // razón social or full name
	Address     string
	Email       string
}

type SaleLine struct {
	SKU       string
	Name      string
	UnitPrice money.Money // without IGV
	Qty       int
	LineTotal money.Money // UnitPrice × Qty, without IGV
}

// Sale is what the orders context tells invoicing about a paid order.
type Sale struct {
	OrderID   id.ID
	Number    string
	AccountID id.ID
	Customer  Customer
	Lines     []SaleLine
	Shipping  money.Money // without IGV
	Tax       money.Money // IGV of the whole order
	Total     money.Money
	PaidAt    time.Time
}

// Unit codes of SUNAT catalogue 03.
const (
	UnitGoods   = "NIU" // units of goods
	UnitService = "ZZ"  // services (delivery)
)

// DocLine is one line as SUNAT expects it. Amounts are in minor units.
type DocLine struct {
	Unit        string
	Code        string
	Description string
	Qty         int
	UnitValue   int64 // valor unitario, without IGV
	Subtotal    int64 // UnitValue × Qty
	IGV         int64
	Total       int64 // Subtotal + IGV
}

// UnitPrice is the price with IGV of one unit, with ten decimals as SUNAT
// allows ("precio unitario"); it need not be a whole céntimo.
func (l DocLine) UnitPrice() string {
	scaled := l.Total * 1e8 / int64(l.Qty) // minor units × 1e8 → 10 decimals
	return fmt.Sprintf("%d.%010d", scaled/1e10, scaled%1e10)
}

// Document is an electronic document ready to send to the OSE.
type Document struct {
	Type         DocType
	Series       string
	Number       int
	IssueDate    time.Time
	Currency     money.Currency
	ExchangeRate string // PEN per USD, required for USD documents
	Customer     Customer
	Lines        []DocLine
	TaxedTotal   int64 // total gravada
	IGV          int64
	Total        int64
	// Credit notes
	RefType   DocType
	RefSeries string
	RefNumber int
	Reason    string
}

// lima is Peru's time zone: the issue date is the date in Lima.
var lima = time.FixedZone("PET", -5*60*60)

// BuildDocument turns a sale into document lines. IGV is computed per line
// and the rounding difference goes to the largest line, so lines add up to
// exactly the IGV the customer paid (SUNAT checks the sums).
func BuildDocument(inv *Invoice, s Sale, ref *Invoice, reason string, exchangeRate string, now time.Time) (Document, error) {
	if s.Customer.DocNumber == "" || s.Customer.Name == "" {
		return Document{}, apperr.Invalid("invoicing.customer_missing", "billing", "the order has no billing document")
	}
	if s.Total.Currency == money.USD && exchangeRate == "" {
		return Document{}, apperr.Invalid("invoicing.exchange_rate_missing", "currency", "USD documents need an exchange rate")
	}
	d := Document{
		Type: inv.Type, Series: inv.Series, Number: inv.Number, IssueDate: now.In(lima),
		Currency: s.Total.Currency, Customer: s.Customer,
		IGV: s.Tax.Minor, Total: s.Total.Minor,
	}
	if s.Total.Currency == money.USD {
		d.ExchangeRate = exchangeRate
	}
	for _, l := range s.Lines {
		d.Lines = append(d.Lines, DocLine{
			Unit: UnitGoods, Code: l.SKU, Description: strings.TrimSpace(l.Name), Qty: l.Qty,
			UnitValue: l.UnitPrice.Minor, Subtotal: l.LineTotal.Minor,
		})
	}
	if s.Shipping.Minor > 0 {
		d.Lines = append(d.Lines, DocLine{
			Unit: UnitService, Code: "ENVIO", Description: "Servicio de envío", Qty: 1,
			UnitValue: s.Shipping.Minor, Subtotal: s.Shipping.Minor,
		})
	}
	if len(d.Lines) == 0 {
		return Document{}, apperr.Invalid("invoicing.no_lines", "lines", "the order has no lines")
	}
	var igvSum int64
	largest := 0
	for i := range d.Lines {
		l := &d.Lines[i]
		d.TaxedTotal += l.Subtotal
		l.IGV = (l.Subtotal*igvPercent + 50) / 100
		igvSum += l.IGV
		if l.Subtotal > d.Lines[largest].Subtotal {
			largest = i
		}
	}
	d.Lines[largest].IGV += d.IGV - igvSum
	for i := range d.Lines {
		d.Lines[i].Total = d.Lines[i].Subtotal + d.Lines[i].IGV
	}
	if d.TaxedTotal+d.IGV != d.Total {
		return Document{}, fmt.Errorf("order %s: lines (%d + IGV %d) do not add up to the total %d", s.Number, d.TaxedTotal, d.IGV, d.Total)
	}
	if inv.Type == CreditNote {
		if ref == nil {
			return Document{}, fmt.Errorf("credit note without the document it modifies")
		}
		d.RefType, d.RefSeries, d.RefNumber = ref.Type, ref.Series, ref.Number
		d.Reason = strings.TrimSpace(reason)
		if d.Reason == "" {
			d.Reason = "Anulación de la operación"
		}
	}
	return d, nil
}
