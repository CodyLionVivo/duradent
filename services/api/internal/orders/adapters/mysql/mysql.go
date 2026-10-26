// Package mysql stores carts, orders and payments. Every method joins the
// transaction carried by ctx when present (platform/mysql.Conn).
package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/orders/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
)

type (
	Carts       struct{ db *sql.DB }
	Orders      struct{ db *sql.DB }
	Payments    struct{ db *sql.DB }
	Idempotency struct{ db *sql.DB }
)

type Stores struct {
	Carts       *Carts
	Orders      *Orders
	Payments    *Payments
	Idempotency *Idempotency
}

func New(db *sql.DB) Stores {
	return Stores{&Carts{db}, &Orders{db}, &Payments{db}, &Idempotency{db}}
}

var (
	_ ports.CartRepository    = (*Carts)(nil)
	_ ports.OrderRepository   = (*Orders)(nil)
	_ ports.PaymentRepository = (*Payments)(nil)
	_ ports.Idempotency       = (*Idempotency)(nil)
)

type nullID struct{ ID id.ID }

func (n *nullID) Scan(src any) error {
	if src == nil {
		n.ID = id.Nil
		return nil
	}
	return n.ID.Scan(src)
}

func optional(v id.ID) any {
	if v.IsNil() {
		return nil
	}
	return v
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// --- carts ---------------------------------------------------------------------------

func (r *Carts) one(ctx context.Context, where string, arg any) (*domain.Cart, error) {
	q := mysql.Conn(ctx, r.db)
	c := &domain.Cart{}
	var acc nullID
	var key sql.NullString
	err := q.QueryRowContext(ctx, "SELECT id, account_id, cart_key, updated_at FROM carts WHERE "+where, arg).Scan(&c.ID, &acc, &key, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("orders.cart_not_found", "no cart")
	}
	if err != nil {
		return nil, err
	}
	c.AccountID, c.Key = acc.ID, key.String
	rows, err := q.QueryContext(ctx, "SELECT variant_id, qty FROM cart_items WHERE cart_id = ? ORDER BY position", c.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var it domain.CartItem
		if err := rows.Scan(&it.VariantID, &it.Qty); err != nil {
			return nil, err
		}
		c.Items = append(c.Items, it)
	}
	return c, rows.Err()
}

func (r *Carts) ByAccount(ctx context.Context, accountID id.ID) (*domain.Cart, error) {
	return r.one(ctx, "account_id = ?", accountID)
}

func (r *Carts) ByKey(ctx context.Context, key string) (*domain.Cart, error) {
	return r.one(ctx, "cart_key = ?", key)
}

func (r *Carts) Save(ctx context.Context, c *domain.Cart) error {
	q := mysql.Conn(ctx, r.db)
	if _, err := q.ExecContext(ctx, `INSERT INTO carts (id, account_id, cart_key, updated_at) VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE updated_at = VALUES(updated_at)`, c.ID, optional(c.AccountID), nullString(c.Key), c.UpdatedAt); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, "DELETE FROM cart_items WHERE cart_id = ?", c.ID); err != nil {
		return err
	}
	for i, it := range c.Items {
		if _, err := q.ExecContext(ctx, "INSERT INTO cart_items (cart_id, variant_id, qty, position) VALUES (?, ?, ?, ?)", c.ID, it.VariantID, it.Qty, i); err != nil {
			return err
		}
	}
	return nil
}

func (r *Carts) Delete(ctx context.Context, cartID id.ID) error {
	_, err := mysql.Conn(ctx, r.db).ExecContext(ctx, "DELETE FROM carts WHERE id = ?", cartID)
	return err
}

// --- orders --------------------------------------------------------------------------

const orderCols = `id, number, account_id, email, status, payment_method, currency, subtotal, shipping_amount, tax_amount, total,
	billing, shipping, notes, payment_deadline, charging_until, carrier, tracking, cancel_reason,
	created_at, updated_at, paid_at, shipped_at, delivered_at, cancelled_at, refunded_at, refund_ref`

type scanner interface{ Scan(dest ...any) error }

func scanOrder(s scanner) (*domain.Order, error) {
	o := &domain.Order{}
	var cur, sub, ship, tax, total string
	var billing, shipping []byte
	var charging, paid, shipped, delivered, cancelled, refunded sql.NullTime
	if err := s.Scan(&o.ID, &o.Number, &o.AccountID, &o.Email, &o.Status, &o.Method, &cur, &sub, &ship, &tax, &total,
		&billing, &shipping, &o.Notes, &o.PaymentDeadline, &charging, &o.Carrier, &o.Tracking, &o.CancelReason,
		&o.CreatedAt, &o.UpdatedAt, &paid, &shipped, &delivered, &cancelled, &refunded, &o.RefundRef); err != nil {
		return nil, err
	}
	o.Currency = money.Currency(cur)
	var err error
	for _, p := range []struct {
		dst *money.Money
		src string
	}{{&o.Subtotal, sub}, {&o.ShippingAmount, ship}, {&o.Tax, tax}, {&o.Total, total}} {
		if *p.dst, err = money.Parse(p.src, o.Currency); err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal(billing, &o.Billing); err != nil {
		return nil, fmt.Errorf("order billing: %w", err)
	}
	if err := json.Unmarshal(shipping, &o.Shipping); err != nil {
		return nil, fmt.Errorf("order shipping: %w", err)
	}
	ptr := func(t sql.NullTime) *time.Time {
		if !t.Valid {
			return nil
		}
		v := t.Time
		return &v
	}
	o.ChargingUntil, o.PaidAt, o.ShippedAt, o.DeliveredAt, o.CancelledAt = ptr(charging), ptr(paid), ptr(shipped), ptr(delivered), ptr(cancelled)
	o.RefundedAt = ptr(refunded)
	return o, nil
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func (r *Orders) Create(ctx context.Context, o *domain.Order) error {
	q := mysql.Conn(ctx, r.db)
	billing, _ := json.Marshal(o.Billing)
	shipping, _ := json.Marshal(o.Shipping)
	if _, err := q.ExecContext(ctx, "INSERT INTO orders ("+orderCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.Number, o.AccountID, o.Email, o.Status, o.Method, string(o.Currency), o.Subtotal.Decimal(), o.ShippingAmount.Decimal(),
		o.Tax.Decimal(), o.Total.Decimal(), billing, shipping, o.Notes, o.PaymentDeadline, nullTime(o.ChargingUntil),
		o.Carrier, o.Tracking, o.CancelReason, o.CreatedAt, o.UpdatedAt,
		nullTime(o.PaidAt), nullTime(o.ShippedAt), nullTime(o.DeliveredAt), nullTime(o.CancelledAt), nullTime(o.RefundedAt), o.RefundRef); err != nil {
		return err
	}
	for i, l := range o.Lines {
		if _, err := q.ExecContext(ctx, `INSERT INTO order_items (order_id, line_no, variant_id, sku, name, options, unit_price, qty, line_total)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, o.ID, i+1, l.VariantID, l.SKU, l.Name, l.Options, l.UnitPrice.Decimal(), l.Qty, l.LineTotal.Decimal()); err != nil {
			return err
		}
	}
	return r.appendHistory(ctx, q, o)
}

func (r *Orders) appendHistory(ctx context.Context, q mysql.Querier, o *domain.Order) error {
	for _, h := range o.History {
		if _, err := q.ExecContext(ctx, "INSERT INTO order_status_history (id, order_id, from_status, to_status, actor, note, at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			id.New(), o.ID, string(h.From), string(h.To), h.Actor, truncate(h.Note, 200), h.At); err != nil {
			return err
		}
	}
	o.History = nil
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (r *Orders) Update(ctx context.Context, o *domain.Order) error {
	q := mysql.Conn(ctx, r.db)
	if _, err := q.ExecContext(ctx, `UPDATE orders SET status = ?, charging_until = ?, carrier = ?, tracking = ?, cancel_reason = ?,
		updated_at = ?, paid_at = ?, shipped_at = ?, delivered_at = ?, cancelled_at = ?, refunded_at = ?, refund_ref = ? WHERE id = ?`,
		o.Status, nullTime(o.ChargingUntil), o.Carrier, o.Tracking, o.CancelReason, o.UpdatedAt,
		nullTime(o.PaidAt), nullTime(o.ShippedAt), nullTime(o.DeliveredAt), nullTime(o.CancelledAt), nullTime(o.RefundedAt), o.RefundRef, o.ID); err != nil {
		return err
	}
	return r.appendHistory(ctx, q, o)
}

func (r *Orders) load(ctx context.Context, where string, arg any, lock bool) (*domain.Order, error) {
	q := mysql.Conn(ctx, r.db)
	query := "SELECT " + orderCols + " FROM orders WHERE " + where
	if lock {
		query += " FOR UPDATE"
	}
	o, err := scanOrder(q.QueryRowContext(ctx, query, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("orders.order_not_found", "order not found")
	}
	if err != nil {
		return nil, err
	}
	return o, r.loadLines(ctx, q, []*domain.Order{o})
}

func (r *Orders) loadLines(ctx context.Context, q mysql.Querier, orders []*domain.Order) error {
	if len(orders) == 0 {
		return nil
	}
	byID := make(map[id.ID]*domain.Order, len(orders))
	args := make([]any, len(orders))
	for i, o := range orders {
		byID[o.ID] = o
		args[i] = o.ID
	}
	rows, err := q.QueryContext(ctx, `SELECT order_id, variant_id, sku, name, options, unit_price, qty, line_total FROM order_items
		WHERE order_id IN (`+mysql.Placeholders(len(args))+`) ORDER BY order_id, line_no`, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var oid id.ID
		var l domain.Line
		var unit, total string
		if err := rows.Scan(&oid, &l.VariantID, &l.SKU, &l.Name, &l.Options, &unit, &l.Qty, &total); err != nil {
			return err
		}
		o := byID[oid]
		if l.UnitPrice, err = money.Parse(unit, o.Currency); err != nil {
			return err
		}
		if l.LineTotal, err = money.Parse(total, o.Currency); err != nil {
			return err
		}
		o.Lines = append(o.Lines, l)
	}
	return rows.Err()
}

func (r *Orders) ByID(ctx context.Context, orderID id.ID) (*domain.Order, error) {
	return r.load(ctx, "id = ?", orderID, false)
}

func (r *Orders) ByNumber(ctx context.Context, number string) (*domain.Order, error) {
	return r.load(ctx, "number = ?", number, false)
}

func (r *Orders) Lock(ctx context.Context, orderID id.ID) (*domain.Order, error) {
	return r.load(ctx, "id = ?", orderID, true)
}

func (r *Orders) List(ctx context.Context, f ports.OrderFilter) ([]*domain.Order, int, error) {
	q := mysql.Conn(ctx, r.db)
	var where []string
	var args []any
	if !f.AccountID.IsNil() {
		where = append(where, "account_id = ?")
		args = append(args, f.AccountID)
	}
	if len(f.Statuses) > 0 {
		where = append(where, "status IN ("+mysql.Placeholders(len(f.Statuses))+")")
		for _, s := range f.Statuses {
			args = append(args, string(s))
		}
	}
	if f.Query != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(f.Query) + "%"
		where = append(where, "(number LIKE ? OR email LIKE ? OR JSON_UNQUOTE(JSON_EXTRACT(billing, '$.document_number')) LIKE ? OR JSON_UNQUOTE(JSON_EXTRACT(billing, '$.business_name')) LIKE ?)")
		args = append(args, like, like, like, like)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM orders"+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if f.Limit <= 0 {
		f.Limit = 20
	}
	out, err := scanOrders(ctx, q, "SELECT "+orderCols+" FROM orders"+cond+" ORDER BY created_at DESC LIMIT ? OFFSET ?", append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	return out, total, r.loadLines(ctx, q, out)
}

// scanOrders reads order rows (without lines); rows are closed before the
// caller runs further queries on the same connection.
func scanOrders(ctx context.Context, q mysql.Querier, query string, args ...any) ([]*domain.Order, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *Orders) History(ctx context.Context, orderID id.ID) ([]domain.StatusChange, error) {
	rows, err := mysql.Conn(ctx, r.db).QueryContext(ctx,
		"SELECT from_status, to_status, actor, note, at FROM order_status_history WHERE order_id = ? ORDER BY at, id", orderID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.StatusChange
	for rows.Next() {
		var h domain.StatusChange
		var from, to string
		if err := rows.Scan(&from, &to, &h.Actor, &h.Note, &h.At); err != nil {
			return nil, err
		}
		h.From, h.To = domain.Status(from), domain.Status(to)
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r *Orders) Overdue(ctx context.Context, t time.Time, limit int) ([]id.ID, error) {
	rows, err := mysql.Conn(ctx, r.db).QueryContext(ctx,
		"SELECT id FROM orders WHERE status = 'pending_payment' AND payment_deadline <= ? ORDER BY payment_deadline LIMIT ?", t, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []id.ID
	for rows.Next() {
		var v id.ID
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (r *Orders) NextNumber(ctx context.Context) (string, error) {
	res, err := mysql.Conn(ctx, r.db).ExecContext(ctx, "INSERT INTO order_numbers () VALUES ()")
	if err != nil {
		return "", err
	}
	n, err := res.LastInsertId()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("DD-%06d", n), nil
}

// --- payments ------------------------------------------------------------------------

func (r *Payments) Add(ctx context.Context, p domain.Payment) error {
	_, err := mysql.Conn(ctx, r.db).ExecContext(ctx, `INSERT INTO payments (id, order_id, method, status, amount, currency, provider_ref,
		source_type, failure_code, failure_message, reference, confirmed_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.OrderID, p.Method, p.Status, p.Amount.Decimal(), string(p.Amount.Currency), p.ProviderRef, p.SourceType,
		truncate(p.FailureCode, 60), truncate(p.FailureMessage, 300), p.Reference, optional(p.ConfirmedBy), p.CreatedAt)
	if key, dup := mysql.DuplicateKey(err); dup && key == "uk_payments_confirmed_ref" {
		return apperr.Conflict("orders.payment_duplicate", "provider_ref", "this charge was already recorded")
	}
	return err
}

func (r *Payments) ByOrder(ctx context.Context, orderID id.ID) ([]domain.Payment, error) {
	rows, err := mysql.Conn(ctx, r.db).QueryContext(ctx, `SELECT id, order_id, method, status, amount, currency, provider_ref, source_type,
		failure_code, failure_message, reference, confirmed_by, created_at FROM payments WHERE order_id = ? ORDER BY created_at`, orderID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []domain.Payment
	for rows.Next() {
		var p domain.Payment
		var amount, cur string
		var by nullID
		if err := rows.Scan(&p.ID, &p.OrderID, &p.Method, &p.Status, &amount, &cur, &p.ProviderRef, &p.SourceType,
			&p.FailureCode, &p.FailureMessage, &p.Reference, &by, &p.CreatedAt); err != nil {
			return nil, err
		}
		if p.Amount, err = money.Parse(amount, money.Currency(cur)); err != nil {
			return nil, err
		}
		p.ConfirmedBy = by.ID
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Payments) ConfirmedByRef(ctx context.Context, providerRef string) (bool, error) {
	var one int
	err := mysql.Conn(ctx, r.db).QueryRowContext(ctx, "SELECT 1 FROM payments WHERE confirmed_ref = ?", providerRef).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// --- idempotency ---------------------------------------------------------------------

func hashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (r *Idempotency) Lookup(ctx context.Context, scope, key, requestHash string) (string, bool, error) {
	var hash string
	var body sql.NullString
	err := mysql.Conn(ctx, r.db).QueryRowContext(ctx,
		"SELECT request_hash, JSON_UNQUOTE(JSON_EXTRACT(response_body, '$.id')) FROM idempotency_keys WHERE scope = ? AND idem_key = ? AND expires_at > ?",
		truncate(scope, 64), hashKey(key), time.Now().UTC()).Scan(&hash, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if hash != requestHash {
		return "", false, apperr.Conflict("orders.idempotency_mismatch", "Idempotency-Key", "this key was already used for a different request")
	}
	return body.String, body.Valid, nil
}

func (r *Idempotency) Store(ctx context.Context, scope, key, requestHash, resultID string, expires time.Time) error {
	body, _ := json.Marshal(map[string]string{"id": resultID})
	_, err := mysql.Conn(ctx, r.db).ExecContext(ctx, `INSERT INTO idempotency_keys (idem_key, scope, request_hash, response_code, response_body, created_at, expires_at)
		VALUES (?, ?, ?, 201, ?, ?, ?)`, hashKey(key), truncate(scope, 64), requestHash, body, time.Now().UTC(), expires)
	if _, dup := mysql.DuplicateKey(err); dup {
		return apperr.Conflict("orders.idempotency_race", "Idempotency-Key", "a request with this key is already being processed")
	}
	return err
}
