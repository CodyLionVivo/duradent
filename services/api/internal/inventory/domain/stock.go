// Package domain models stock per variant. Placing an order reserves units;
// payment confirms the reservation (units leave the shelf); cancellation or
// expiry releases it.
package domain

import (
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

type StockItem struct {
	VariantID    id.ID
	OnHand       int // physically in the warehouse
	Reserved     int // promised to unpaid or unshipped orders
	ReorderPoint int // alert threshold
	UpdatedAt    time.Time
}

func (s StockItem) Available() int { return s.OnHand - s.Reserved }

// Low reports whether stock fell to the reorder point.
func (s StockItem) Low() bool { return s.ReorderPoint > 0 && s.Available() <= s.ReorderPoint }

type ReservationStatus string

const (
	ReservationActive    ReservationStatus = "active"
	ReservationConfirmed ReservationStatus = "confirmed"
	ReservationReleased  ReservationStatus = "released"
	ReservationReturned  ReservationStatus = "returned" // sold, then refunded and put back on the shelf
)

type Reservation struct {
	ID        id.ID
	OrderID   id.ID
	VariantID id.ID
	Qty       int
	Status    ReservationStatus
	CreatedAt time.Time
}

type Line struct {
	VariantID id.ID
	Qty       int
}

// MovementReason explains every change to on-hand stock (audit trail).
type MovementReason string

const (
	MovementSale       MovementReason = "sale"
	MovementAdjustment MovementReason = "adjustment"
	MovementPurchase   MovementReason = "purchase"
	MovementReturn     MovementReason = "return"
)

func ParseReason(s string) (MovementReason, error) {
	switch r := MovementReason(s); r {
	case MovementAdjustment, MovementPurchase, MovementReturn:
		return r, nil
	}
	return "", apperr.Invalid("inventory.reason_invalid", "reason", "use adjustment, purchase or return")
}

type Movement struct {
	ID        id.ID
	VariantID id.ID
	Delta     int
	Reason    MovementReason
	Reference string
	CreatedAt time.Time
}

// Reserve takes qty units out of availability.
func (s *StockItem) Reserve(qty int, now time.Time) error {
	if qty <= 0 {
		return apperr.Invalid("inventory.qty_invalid", "qty", "quantity must be positive")
	}
	if s.Available() < qty {
		return apperr.Conflict("inventory.insufficient_stock", s.VariantID.String(), "not enough stock")
	}
	s.Reserved += qty
	s.UpdatedAt = now
	return nil
}

// Commit ships reserved units: they leave both on-hand and reserved.
func (s *StockItem) Commit(qty int, now time.Time) {
	s.OnHand -= qty
	s.Reserved -= qty
	s.UpdatedAt = now
}

// Return puts sold units back on hand (refunded order).
func (s *StockItem) Return(qty int, now time.Time) {
	s.OnHand += qty
	s.UpdatedAt = now
}

// Release returns reserved units to availability.
func (s *StockItem) Release(qty int, now time.Time) {
	s.Reserved -= qty
	if s.Reserved < 0 {
		s.Reserved = 0
	}
	s.UpdatedAt = now
}

// Adjust changes on-hand stock (count correction, purchase, return). Stock can
// never drop below what is already promised to orders.
func (s *StockItem) Adjust(delta int, now time.Time) error {
	if delta == 0 {
		return apperr.Invalid("inventory.delta_zero", "delta", "the change cannot be zero")
	}
	if s.OnHand+delta < s.Reserved {
		return apperr.Conflict("inventory.below_reserved", "delta", "stock cannot go below the units reserved for orders")
	}
	s.OnHand += delta
	s.UpdatedAt = now
	return nil
}

func (s *StockItem) SetReorderPoint(n int, now time.Time) error {
	if n < 0 || n > 100_000 {
		return apperr.Invalid("inventory.reorder_invalid", "reorder_point", "reorder point must be between 0 and 100000")
	}
	s.ReorderPoint = n
	s.UpdatedAt = now
	return nil
}
