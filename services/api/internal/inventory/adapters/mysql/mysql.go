// Package mysql stores stock in MySQL. Every method uses the transaction
// carried by ctx when present (see platform/mysql.Conn).
package mysql

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/inventory/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/inventory/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
)

type Stock struct{ db *sql.DB }

func New(db *sql.DB) *Stock { return &Stock{db: db} }

var _ ports.StockRepository = (*Stock)(nil)

func sortedArgs(ids []id.ID) []any {
	sorted := slices.Clone(ids)
	slices.SortFunc(sorted, func(a, b id.ID) int { return strings.Compare(a.String(), b.String()) })
	sorted = slices.Compact(sorted)
	args := make([]any, len(sorted))
	for i, v := range sorted {
		args[i] = v
	}
	return args
}

func (s *Stock) query(ctx context.Context, ids []id.ID, lock bool) (map[id.ID]*domain.StockItem, error) {
	out := make(map[id.ID]*domain.StockItem, len(ids))
	for _, v := range ids {
		out[v] = &domain.StockItem{VariantID: v}
	}
	if len(ids) == 0 {
		return out, nil
	}
	args := sortedArgs(ids)
	q := "SELECT variant_id, on_hand, reserved, reorder_point, updated_at FROM stock_items WHERE variant_id IN (" +
		mysql.Placeholders(len(args)) + ") ORDER BY variant_id"
	if lock {
		q += " FOR UPDATE"
	}
	rows, err := mysql.Conn(ctx, s.db).QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		it := &domain.StockItem{}
		if err := rows.Scan(&it.VariantID, &it.OnHand, &it.Reserved, &it.ReorderPoint, &it.UpdatedAt); err != nil {
			return nil, err
		}
		out[it.VariantID] = it
	}
	return out, rows.Err()
}

func (s *Stock) Lock(ctx context.Context, variantIDs []id.ID) (map[id.ID]*domain.StockItem, error) {
	return s.query(ctx, variantIDs, true)
}

func (s *Stock) Get(ctx context.Context, variantIDs []id.ID) (map[id.ID]domain.StockItem, error) {
	items, err := s.query(ctx, variantIDs, false)
	if err != nil {
		return nil, err
	}
	out := make(map[id.ID]domain.StockItem, len(items))
	for k, v := range items {
		out[k] = *v
	}
	return out, nil
}

func (s *Stock) Save(ctx context.Context, items ...*domain.StockItem) error {
	q := mysql.Conn(ctx, s.db)
	for _, it := range items {
		updated := it.UpdatedAt
		if updated.IsZero() {
			updated = time.Now().UTC()
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO stock_items (variant_id, on_hand, reserved, reorder_point, updated_at) VALUES (?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE on_hand = VALUES(on_hand), reserved = VALUES(reserved),
				reorder_point = VALUES(reorder_point), updated_at = VALUES(updated_at)`,
			it.VariantID, it.OnHand, it.Reserved, it.ReorderPoint, updated); err != nil {
			return err
		}
	}
	return nil
}

func (s *Stock) AddReservations(ctx context.Context, rs []domain.Reservation) error {
	q := mysql.Conn(ctx, s.db)
	for _, r := range rs {
		if _, err := q.ExecContext(ctx, "INSERT INTO stock_reservations (id, order_id, variant_id, qty, status, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			r.ID, r.OrderID, r.VariantID, r.Qty, r.Status, r.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Stock) ActiveReservations(ctx context.Context, orderID id.ID) ([]domain.Reservation, error) {
	rows, err := mysql.Conn(ctx, s.db).QueryContext(ctx,
		"SELECT id, order_id, variant_id, qty, status, created_at FROM stock_reservations WHERE order_id = ? AND status = 'active' FOR UPDATE", orderID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Reservation
	for rows.Next() {
		var r domain.Reservation
		if err := rows.Scan(&r.ID, &r.OrderID, &r.VariantID, &r.Qty, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Stock) SetReservationStatus(ctx context.Context, orderID id.ID, from, to domain.ReservationStatus) error {
	_, err := mysql.Conn(ctx, s.db).ExecContext(ctx, "UPDATE stock_reservations SET status = ? WHERE order_id = ? AND status = ?", to, orderID, from)
	return err
}

func (s *Stock) AddMovements(ctx context.Context, ms []domain.Movement) error {
	q := mysql.Conn(ctx, s.db)
	for _, m := range ms {
		if _, err := q.ExecContext(ctx, "INSERT INTO stock_movements (id, variant_id, delta, reason, reference, created_at) VALUES (?, ?, ?, ?, ?, ?)",
			m.ID, m.VariantID, m.Delta, m.Reason, m.Reference, m.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Stock) Movements(ctx context.Context, variantID id.ID, limit int) ([]domain.Movement, error) {
	rows, err := mysql.Conn(ctx, s.db).QueryContext(ctx,
		"SELECT id, variant_id, delta, reason, reference, created_at FROM stock_movements WHERE variant_id = ? ORDER BY created_at DESC LIMIT ?", variantID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Movement
	for rows.Next() {
		var m domain.Movement
		if err := rows.Scan(&m.ID, &m.VariantID, &m.Delta, &m.Reason, &m.Reference, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
