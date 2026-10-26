// Package ports declares the inventory's persistence and side effects.
package ports

import (
	"context"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/inventory/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

// StockRepository persists stock. Methods take part in the transaction
// carried by ctx, if any.
type StockRepository interface {
	// Lock returns the items for update, in variant id order (so concurrent
	// orders always lock rows in the same order and cannot deadlock).
	// Variants without a row are returned with zero stock.
	Lock(ctx context.Context, variantIDs []id.ID) (map[id.ID]*domain.StockItem, error)
	Get(ctx context.Context, variantIDs []id.ID) (map[id.ID]domain.StockItem, error)
	Save(ctx context.Context, items ...*domain.StockItem) error
	AddReservations(ctx context.Context, rs []domain.Reservation) error
	// ActiveReservations locks the order's active reservations.
	ActiveReservations(ctx context.Context, orderID id.ID) ([]domain.Reservation, error)
	SetReservationStatus(ctx context.Context, orderID id.ID, from, to domain.ReservationStatus) error
	AddMovements(ctx context.Context, ms []domain.Movement) error
	Movements(ctx context.Context, variantID id.ID, limit int) ([]domain.Movement, error)
}

type UnitOfWork interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Events records domain events in the current transaction (outbox).
type Events interface {
	Record(ctx context.Context, aggregateType string, aggregateID id.ID, eventType string, payload any, at time.Time) error
}
