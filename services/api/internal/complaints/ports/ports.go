// Package ports declares what the complaints context needs: storage for the
// sheets and their yearly counter, a unit of work and the outbox.
package ports

import (
	"context"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/complaints/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

type Filter struct {
	Status domain.Status // empty: any
	Query  string        // sheet number, name, email or document
	Limit  int
	Offset int
}

// Complaints stores sheets; every method joins the transaction in ctx.
type Complaints interface {
	// NextNumber takes the next correlative of a year (inside a transaction).
	NextNumber(ctx context.Context, year int) (int, error)
	// Create stores a new sheet. Conflict if its idempotency key was used.
	Create(ctx context.Context, c *domain.Complaint, idempotencyKey string) error
	// SaveAnswer stores the answer of an open sheet; Conflict if someone
	// answered it in the meantime.
	SaveAnswer(ctx context.Context, c *domain.Complaint) error
	ByID(ctx context.Context, complaintID id.ID) (*domain.Complaint, error)
	// ByIdempotencyKey returns NotFound if no sheet was filed with that key.
	ByIdempotencyKey(ctx context.Context, key string) (*domain.Complaint, error)
	List(ctx context.Context, f Filter) ([]*domain.Complaint, int, error)
	// CountOpen counts unanswered sheets and how many of them are late.
	CountOpen(ctx context.Context, now time.Time) (open, overdue int, err error)
}

type UnitOfWork interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type Events interface {
	Record(ctx context.Context, aggregateType string, aggregateID id.ID, eventType string, payload any, at time.Time) error
}
