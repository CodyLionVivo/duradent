// Package ports declares what invoicing needs: the paid order (orders
// context), its own storage, the OSE that issues documents, and the outbox.
package ports

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

type Event struct {
	ID          id.ID
	Type        string
	AggregateID id.ID
	Payload     json.RawMessage
}

type Orders interface {
	Sale(ctx context.Context, orderID id.ID) (domain.Sale, error)
	// Owner returns the order id and owner of an order number (NotFound if none).
	Owner(ctx context.Context, number string) (orderID, accountID id.ID, err error)
}

type Filter struct {
	Status domain.Status // empty: any
	Limit  int
	Offset int
}

// Invoices stores documents; every method joins the transaction in ctx.
type Invoices interface {
	// NextNumber takes the next correlative of a series (inside a transaction).
	NextNumber(ctx context.Context, series string) (int, error)
	Create(ctx context.Context, inv *domain.Invoice) error // Conflict if the order already has one of that type
	Update(ctx context.Context, inv *domain.Invoice) error
	ByID(ctx context.Context, invoiceID id.ID) (*domain.Invoice, error)
	ByOrder(ctx context.Context, orderID id.ID) ([]*domain.Invoice, error)
	List(ctx context.Context, f Filter) ([]*domain.Invoice, int, error)
	// Stale returns pending documents not touched since before t.
	Stale(ctx context.Context, before time.Time, limit int) ([]*domain.Invoice, error)
}

// Rejection is a definitive "no" from the OSE (invalid data). Other errors
// are transient and retried.
type Rejection struct {
	Code    string
	Message string
}

func (r *Rejection) Error() string { return "rejected by the OSE: " + r.Code + " " + r.Message }

// ErrAlreadyExists means the OSE already has this series and number (a
// previous attempt went through but its answer was lost).
var ErrAlreadyExists = errors.New("document already registered")

type Provider interface {
	Issue(ctx context.Context, d domain.Document) (domain.Result, error)
	Lookup(ctx context.Context, t domain.DocType, series string, number int) (domain.Result, error)
	Name() string
}

type UnitOfWork interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type Events interface {
	Record(ctx context.Context, aggregateType string, aggregateID id.ID, eventType string, payload any, at time.Time) error
}
