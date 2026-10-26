// Package domain models SUNAT electronic documents: facturas (customers with
// RUC), boletas (everyone else) and the credit notes that cancel them when an
// order is refunded. Documents are issued through an OSE/PSE (Nubefact).
package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

type DocType string

const (
	Factura     DocType = "factura"
	Boleta      DocType = "boleta"
	CreditNote  DocType = "nota_credito"
	igvPercent          = 18
	maxAttempts         = 10
)

type Status string

const (
	StatusPending  Status = "pending"  // number taken, not yet accepted by the OSE
	StatusAccepted Status = "accepted" // registered; PDF/XML available
	StatusRejected Status = "rejected" // validation error: needs a person
)

// Series are the four-character prefixes SUNAT authorised for the company.
type Series struct {
	Factura     string // F001
	Boleta      string // B001
	CreditNoteF string // FC01: credit notes of facturas
	CreditNoteB string // BC01: credit notes of boletas
}

func (s Series) Validate() error {
	for _, v := range []struct{ name, val, prefix string }{
		{"factura", s.Factura, "F"}, {"boleta", s.Boleta, "B"}, {"credit note F", s.CreditNoteF, "F"}, {"credit note B", s.CreditNoteB, "B"},
	} {
		if len(v.val) != 4 || !strings.HasPrefix(v.val, v.prefix) {
			return fmt.Errorf("invoice series %s must be 4 characters starting with %s, got %q", v.name, v.prefix, v.val)
		}
	}
	return nil
}

type Invoice struct {
	ID               id.ID
	OrderID          id.ID
	OrderNumber      string
	Type             DocType
	Series           string
	Number           int
	RefInvoiceID     id.ID // credit notes: the document they cancel
	Currency         money.Currency
	Total            money.Money
	Status           Status
	Attempts         int
	SunatDescription string
	Hash             string
	PDFURL           string
	XMLURL           string
	CDRURL           string
	LastError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Name is how the document is called on the customer's email and in admin.
func (i *Invoice) Name() string {
	label := "Nota de crédito"
	switch i.Type {
	case Factura:
		label = "Factura"
	case Boleta:
		label = "Boleta"
	case CreditNote:
	}
	return fmt.Sprintf("%s %s-%d", label, i.Series, i.Number)
}

// SaleType picks factura or boleta from the order's billing snapshot.
func SaleType(c Customer) DocType {
	if c.InvoiceType == "01" && c.DocType == "RUC" {
		return Factura
	}
	return Boleta
}

// SeriesFor returns the series of a new document.
func (s Series) For(t DocType, ref *Invoice) string {
	switch t {
	case Factura:
		return s.Factura
	case Boleta:
		return s.Boleta
	case CreditNote:
	}
	if ref != nil && ref.Type == Factura {
		return s.CreditNoteF
	}
	return s.CreditNoteB
}

func NewInvoice(orderID id.ID, orderNumber string, t DocType, series string, number int, total money.Money, ref *Invoice, now time.Time) *Invoice {
	inv := &Invoice{
		ID: id.New(), OrderID: orderID, OrderNumber: orderNumber, Type: t, Series: series, Number: number,
		Currency: total.Currency, Total: total, Status: StatusPending, CreatedAt: now, UpdatedAt: now,
	}
	if ref != nil {
		inv.RefInvoiceID = ref.ID
	}
	return inv
}

// Result is what the OSE answered for an accepted document.
type Result struct {
	SunatDescription string
	Hash             string
	PDFURL           string
	XMLURL           string
	CDRURL           string
}

func (i *Invoice) Accept(r Result, now time.Time) {
	i.Status = StatusAccepted
	i.Attempts++
	i.SunatDescription, i.Hash, i.PDFURL, i.XMLURL, i.CDRURL = r.SunatDescription, r.Hash, r.PDFURL, r.XMLURL, r.CDRURL
	i.LastError = ""
	i.UpdatedAt = now
}

func (i *Invoice) Reject(reason string, now time.Time) {
	i.Status = StatusRejected
	i.Attempts++
	i.LastError = truncate(reason, 1000)
	i.UpdatedAt = now
}

// Failed records a transient error; the document stays pending with its
// number so the retry sends the same correlative.
func (i *Invoice) Failed(reason string, now time.Time) {
	i.Attempts++
	i.LastError = truncate(reason, 1000)
	i.UpdatedAt = now
}

// Retry puts a rejected document back in the queue (after fixing the data
// in the OSE panel or the customer profile).
func (i *Invoice) Retry(now time.Time) error {
	if i.Status != StatusRejected {
		return apperr.Conflict("invoicing.not_rejected", "status", "only rejected documents can be retried")
	}
	i.Status = StatusPending
	i.UpdatedAt = now
	return nil
}

// GivenUp reports whether automatic retries should stop.
func (i *Invoice) GivenUp() bool { return i.Attempts >= maxAttempts }

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
