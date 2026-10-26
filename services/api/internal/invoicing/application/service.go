// Package application issues SUNAT documents for paid orders and credit notes
// for refunded ones.
//
// A document's number is taken and stored (status pending) before calling
// the OSE, so a retry after a timeout sends the same series and number:
// SUNAT requires correlatives without gaps and the OSE answers "already
// exists" for a repeat, which is resolved with a lookup.
package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

type Deps struct {
	Orders   ports.Orders
	Invoices ports.Invoices
	Provider ports.Provider
	UoW      ports.UnitOfWork
	Events   ports.Events
	Log      *slog.Logger
	Now      func() time.Time
}

type Config struct {
	Series domain.Series
	// USDExchangeRate is the PEN/USD rate for USD documents (e.g. "3.750").
	// SUNAT expects the SBS rate of the issue date; operations updates it.
	USDExchangeRate string
}

type Service struct {
	d   Deps
	cfg Config
}

func NewService(d Deps, cfg Config) (*Service, error) {
	if err := cfg.Series.Validate(); err != nil {
		return nil, err
	}
	return &Service{d: d, cfg: cfg}, nil
}

// Events lists the event types this context consumes (SQS filter policy).
var Events = []string{"PaymentConfirmed", "OrderRefunded"}

func (s *Service) Handle(ctx context.Context, e ports.Event) error {
	var p struct {
		OrderID id.ID  `json:"order_id"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("payload of %s: %w", e.Type, err)
	}
	if p.OrderID.IsNil() {
		p.OrderID = e.AggregateID
	}
	switch e.Type {
	case "PaymentConfirmed":
		return s.IssueSale(ctx, p.OrderID)
	case "OrderRefunded":
		return s.IssueCreditNote(ctx, p.OrderID, p.Reason)
	}
	return nil
}

func find(invs []*domain.Invoice, sale bool) *domain.Invoice {
	for _, inv := range invs {
		if (inv.Type == domain.CreditNote) != sale {
			return inv
		}
	}
	return nil
}

// IssueSale emits the factura or boleta of a paid order (once).
func (s *Service) IssueSale(ctx context.Context, orderID id.ID) error {
	sale, err := s.d.Orders.Sale(ctx, orderID)
	if err != nil {
		return err
	}
	t := domain.SaleType(sale.Customer)
	inv, err := s.ensure(ctx, sale, t, nil)
	if err != nil || inv.Status != domain.StatusPending {
		return err
	}
	return s.submit(ctx, inv, sale, nil, "")
}

// IssueCreditNote cancels the sale document of a refunded order.
func (s *Service) IssueCreditNote(ctx context.Context, orderID id.ID, reason string) error {
	invs, err := s.d.Invoices.ByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	ref := find(invs, true)
	switch {
	case ref == nil:
		return errors.New("the sale document does not exist yet") // retried until PaymentConfirmed is handled
	case ref.Status == domain.StatusPending:
		return errors.New("the sale document is still pending")
	case ref.Status == domain.StatusRejected:
		s.d.Log.WarnContext(ctx, "refunded order whose invoice was rejected: no credit note", "order", ref.OrderNumber)
		return nil
	}
	sale, err := s.d.Orders.Sale(ctx, orderID)
	if err != nil {
		return err
	}
	inv, err := s.ensure(ctx, sale, domain.CreditNote, ref)
	if err != nil || inv.Status != domain.StatusPending {
		return err
	}
	return s.submit(ctx, inv, sale, ref, reason)
}

// ensure returns the order's document of that type, creating it (with its
// number) if needed.
func (s *Service) ensure(ctx context.Context, sale domain.Sale, t domain.DocType, ref *domain.Invoice) (*domain.Invoice, error) {
	var out *domain.Invoice
	err := s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		invs, err := s.d.Invoices.ByOrder(ctx, sale.OrderID)
		if err != nil {
			return err
		}
		if out = find(invs, t != domain.CreditNote); out != nil {
			return nil
		}
		series := s.cfg.Series.For(t, ref)
		n, err := s.d.Invoices.NextNumber(ctx, series)
		if err != nil {
			return err
		}
		out = domain.NewInvoice(sale.OrderID, sale.Number, t, series, n, sale.Total, ref, s.d.Now())
		return s.d.Invoices.Create(ctx, out)
	})
	return out, err
}

func (s *Service) submit(ctx context.Context, inv *domain.Invoice, sale domain.Sale, ref *domain.Invoice, reason string) error {
	doc, err := domain.BuildDocument(inv, sale, ref, reason, s.cfg.USDExchangeRate, s.d.Now())
	if err != nil {
		return s.finish(ctx, inv, domain.Result{}, err)
	}
	res, err := s.d.Provider.Issue(ctx, doc)
	if errors.Is(err, ports.ErrAlreadyExists) {
		res, err = s.d.Provider.Lookup(ctx, inv.Type, inv.Series, inv.Number)
	}
	return s.finish(ctx, inv, res, err)
}

// finish stores the outcome. Transient errors are returned so the message is
// retried; rejections are recorded and reported to operations.
func (s *Service) finish(ctx context.Context, inv *domain.Invoice, res domain.Result, issueErr error) error {
	now := s.d.Now()
	var rej *ports.Rejection
	var bad *apperr.Error
	switch {
	case issueErr == nil:
		inv.Accept(res, now)
	case errors.As(issueErr, &rej), errors.As(issueErr, &bad):
		inv.Reject(issueErr.Error(), now)
	default:
		inv.Failed(issueErr.Error(), now)
		if err := s.d.Invoices.Update(ctx, inv); err != nil {
			s.d.Log.ErrorContext(ctx, "store invoice attempt", "error", err)
		}
		return fmt.Errorf("issue %s: %w", inv.Name(), issueErr)
	}
	return s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		if err := s.d.Invoices.Update(ctx, inv); err != nil {
			return err
		}
		if inv.Status == domain.StatusAccepted {
			return s.d.Events.Record(ctx, "invoice", inv.ID, "InvoiceIssued", map[string]any{
				"order_id": inv.OrderID, "invoice_id": inv.ID, "doc_type": inv.Type, "document_name": inv.Name(),
				"series": inv.Series, "number": inv.Number, "pdf_url": inv.PDFURL,
			}, now)
		}
		s.d.Log.ErrorContext(ctx, "invoice rejected", "invoice", inv.Name(), "order", inv.OrderNumber, "error", inv.LastError)
		return s.d.Events.Record(ctx, "invoice", inv.ID, "InvoiceRejected", map[string]any{
			"order_id": inv.OrderID, "invoice_id": inv.ID, "document_name": inv.Name(), "error": inv.LastError,
		}, now)
	})
}

// resubmit sends a pending document again.
func (s *Service) resubmit(ctx context.Context, inv *domain.Invoice) error {
	sale, err := s.d.Orders.Sale(ctx, inv.OrderID)
	if err != nil {
		return err
	}
	var ref *domain.Invoice
	if inv.Type == domain.CreditNote {
		if ref, err = s.d.Invoices.ByID(ctx, inv.RefInvoiceID); err != nil {
			return err
		}
	}
	return s.submit(ctx, inv, sale, ref, "")
}

// RetryStale resends pending documents whose last attempt is older than five
// minutes (an event that ended in the DLQ, a crash). The worker runs it.
func (s *Service) RetryStale(ctx context.Context) (int, error) {
	invs, err := s.d.Invoices.Stale(ctx, s.d.Now().Add(-5*time.Minute), 20)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, inv := range invs {
		if inv.GivenUp() {
			continue
		}
		if err := s.resubmit(ctx, inv); err != nil {
			s.d.Log.WarnContext(ctx, "retry invoice", "invoice", inv.Name(), "error", err)
			continue
		}
		n++
	}
	return n, nil
}

// --- queries and admin --------------------------------------------------------------

// ForCustomer lists the accepted documents of one of the customer's orders.
func (s *Service) ForCustomer(ctx context.Context, accountID id.ID, number string) ([]*domain.Invoice, error) {
	orderID, owner, err := s.d.Orders.Owner(ctx, number)
	if err != nil {
		return nil, err
	}
	if owner != accountID {
		return nil, apperr.NotFound("orders.not_found", "order not found")
	}
	invs, err := s.d.Invoices.ByOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	out := invs[:0]
	for _, inv := range invs {
		if inv.Status == domain.StatusAccepted {
			out = append(out, inv)
		}
	}
	return out, nil
}

func (s *Service) ForOrder(ctx context.Context, orderID id.ID) ([]*domain.Invoice, error) {
	return s.d.Invoices.ByOrder(ctx, orderID)
}

func (s *Service) List(ctx context.Context, f ports.Filter) ([]*domain.Invoice, int, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	return s.d.Invoices.List(ctx, f)
}

// Retry sends a rejected document again after someone fixed the cause.
func (s *Service) Retry(ctx context.Context, invoiceID id.ID) (*domain.Invoice, error) {
	inv, err := s.d.Invoices.ByID(ctx, invoiceID)
	if err != nil {
		return nil, err
	}
	if err := inv.Retry(s.d.Now()); err != nil {
		return nil, err
	}
	inv.Attempts = 0
	if err := s.resubmit(ctx, inv); err != nil {
		s.d.Log.WarnContext(ctx, "retry invoice", "invoice", inv.Name(), "error", err)
	}
	return s.d.Invoices.ByID(ctx, invoiceID)
}
