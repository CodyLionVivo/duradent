// Package application implements stock reservations for orders and stock
// administration.
package application

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/inventory/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/inventory/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

type Service struct {
	stock  ports.StockRepository
	uow    ports.UnitOfWork
	events ports.Events
	now    func() time.Time
}

func NewService(stock ports.StockRepository, uow ports.UnitOfWork, events ports.Events, now func() time.Time) *Service {
	return &Service{stock: stock, uow: uow, events: events, now: now}
}

// Availability returns sellable units per variant (on hand minus reserved).
func (s *Service) Availability(ctx context.Context, variantIDs []id.ID) (map[id.ID]int, error) {
	items, err := s.stock.Get(ctx, variantIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[id.ID]int, len(variantIDs))
	for _, v := range variantIDs {
		out[v] = max(items[v].Available(), 0)
	}
	return out, nil
}

// merge sums quantities per variant and sorts by variant id.
func merge(lines []domain.Line) []domain.Line {
	sum := map[id.ID]int{}
	for _, l := range lines {
		sum[l.VariantID] += l.Qty
	}
	out := make([]domain.Line, 0, len(sum))
	for v, q := range sum {
		out = append(out, domain.Line{VariantID: v, Qty: q})
	}
	slices.SortFunc(out, func(a, b domain.Line) int { return strings.Compare(a.VariantID.String(), b.VariantID.String()) })
	return out
}

// Reserve holds stock for an order. It must run inside the caller's unit of
// work so the order and its reservation commit together. When any line is
// short it reserves nothing and returns a Conflict "inventory.insufficient_stock"
// whose Field lists "variantID:available" pairs.
func (s *Service) Reserve(ctx context.Context, orderID id.ID, lines []domain.Line) error {
	lines = merge(lines)
	ids := make([]id.ID, len(lines))
	for i, l := range lines {
		ids[i] = l.VariantID
	}
	return s.uow.InTx(ctx, func(ctx context.Context) error {
		items, err := s.stock.Lock(ctx, ids)
		if err != nil {
			return err
		}
		now := s.now()
		short := map[id.ID]int{}
		var rs []domain.Reservation
		for _, l := range lines {
			it := items[l.VariantID]
			if err := it.Reserve(l.Qty, now); err != nil {
				if apperr.IsKind(err, apperr.KindConflict) {
					short[l.VariantID] = max(it.Available(), 0)
					continue
				}
				return err
			}
			rs = append(rs, domain.Reservation{ID: id.New(), OrderID: orderID, VariantID: l.VariantID, Qty: l.Qty, Status: domain.ReservationActive, CreatedAt: now})
		}
		if len(short) > 0 {
			return &apperr.Error{Kind: apperr.KindConflict, Code: "inventory.insufficient_stock", Msg: "not enough stock", Field: shortList(short)}
		}
		vals := make([]*domain.StockItem, 0, len(items))
		for _, l := range lines {
			vals = append(vals, items[l.VariantID])
		}
		if err := s.stock.Save(ctx, vals...); err != nil {
			return err
		}
		return s.stock.AddReservations(ctx, rs)
	})
}

// shortList encodes "variantID:available,…" so callers can tell the
// customer which lines to fix.
func shortList(short map[id.ID]int) string {
	parts := make([]string, 0, len(short))
	for v, n := range short {
		parts = append(parts, v.String()+":"+strconv.Itoa(n))
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// Confirm turns an order's reservations into sales (payment received).
func (s *Service) Confirm(ctx context.Context, orderID id.ID) error {
	return s.settle(ctx, orderID, domain.ReservationConfirmed)
}

// Release gives an order's reserved units back (cancelled or unpaid).
func (s *Service) Release(ctx context.Context, orderID id.ID) error {
	return s.settle(ctx, orderID, domain.ReservationReleased)
}

func (s *Service) settle(ctx context.Context, orderID id.ID, to domain.ReservationStatus) error {
	return s.uow.InTx(ctx, func(ctx context.Context) error {
		rs, err := s.stock.ActiveReservations(ctx, orderID)
		if err != nil || len(rs) == 0 {
			return err // nothing reserved (or already settled): idempotent
		}
		lines := make([]domain.Line, len(rs))
		for i, r := range rs {
			lines[i] = domain.Line{VariantID: r.VariantID, Qty: r.Qty}
		}
		lines = merge(lines)
		ids := make([]id.ID, len(lines))
		for i, l := range lines {
			ids[i] = l.VariantID
		}
		items, err := s.stock.Lock(ctx, ids)
		if err != nil {
			return err
		}
		now := s.now()
		var moves []domain.Movement
		vals := make([]*domain.StockItem, 0, len(lines))
		for _, l := range lines {
			it := items[l.VariantID]
			if to == domain.ReservationConfirmed {
				it.Commit(l.Qty, now)
				moves = append(moves, domain.Movement{ID: id.New(), VariantID: l.VariantID, Delta: -l.Qty, Reason: domain.MovementSale, Reference: orderID.String(), CreatedAt: now})
			} else {
				it.Release(l.Qty, now)
			}
			vals = append(vals, it)
		}
		if err := s.stock.Save(ctx, vals...); err != nil {
			return err
		}
		if err := s.stock.SetReservationStatus(ctx, orderID, domain.ReservationActive, to); err != nil {
			return err
		}
		if err := s.stock.AddMovements(ctx, moves); err != nil {
			return err
		}
		for _, it := range vals {
			if to == domain.ReservationConfirmed && it.Low() {
				if err := s.events.Record(ctx, "variant", it.VariantID, "StockLow", map[string]any{
					"variant_id": it.VariantID, "on_hand": it.OnHand, "available": it.Available(), "reorder_point": it.ReorderPoint,
				}, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// --- Admin -------------------------------------------------------------------------

func (s *Service) Stock(ctx context.Context, variantIDs []id.ID) (map[id.ID]domain.StockItem, error) {
	if len(variantIDs) > 500 {
		return nil, apperr.Invalid("inventory.too_many_variants", "variant_id", "at most 500 variants per request")
	}
	return s.stock.Get(ctx, variantIDs)
}

// Adjust changes on-hand stock and records why.
func (s *Service) Adjust(ctx context.Context, variantID id.ID, delta int, reason domain.MovementReason, note string) (domain.StockItem, error) {
	var out domain.StockItem
	err := s.uow.InTx(ctx, func(ctx context.Context) error {
		items, err := s.stock.Lock(ctx, []id.ID{variantID})
		if err != nil {
			return err
		}
		it := items[variantID]
		now := s.now()
		if err := it.Adjust(delta, now); err != nil {
			return err
		}
		if err := s.stock.Save(ctx, it); err != nil {
			return err
		}
		if len(note) > 200 {
			note = note[:200]
		}
		out = *it
		return s.stock.AddMovements(ctx, []domain.Movement{{ID: id.New(), VariantID: variantID, Delta: delta, Reason: reason, Reference: note, CreatedAt: now}})
	})
	return out, err
}

func (s *Service) SetReorderPoint(ctx context.Context, variantID id.ID, n int) (domain.StockItem, error) {
	var out domain.StockItem
	err := s.uow.InTx(ctx, func(ctx context.Context) error {
		items, err := s.stock.Lock(ctx, []id.ID{variantID})
		if err != nil {
			return err
		}
		it := items[variantID]
		if err := it.SetReorderPoint(n, s.now()); err != nil {
			return err
		}
		out = *it
		return s.stock.Save(ctx, it)
	})
	return out, err
}

func (s *Service) Movements(ctx context.Context, variantID id.ID) ([]domain.Movement, error) {
	return s.stock.Movements(ctx, variantID, 50)
}
