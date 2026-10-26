// Package mysql stores electronic documents and their series counters.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/invoicing/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
)

type Invoices struct{ db *sql.DB }

func New(db *sql.DB) *Invoices { return &Invoices{db: db} }

var _ ports.Invoices = (*Invoices)(nil)

// NextNumber increments the series row; the row lock holds until the
// transaction ends, so numbers are handed out without gaps or repeats.
func (r *Invoices) NextNumber(ctx context.Context, series string) (int, error) {
	q := mysql.Conn(ctx, r.db)
	if _, err := q.ExecContext(ctx, `INSERT INTO invoice_series (series, last_number) VALUES (?, LAST_INSERT_ID(1))
		ON DUPLICATE KEY UPDATE last_number = LAST_INSERT_ID(last_number + 1)`, series); err != nil {
		return 0, err
	}
	var n int
	err := q.QueryRowContext(ctx, "SELECT LAST_INSERT_ID()").Scan(&n)
	return n, err
}

const cols = `id, order_id, order_number, doc_type, series, number, ref_invoice_id, currency, total, status, attempts,
	sunat_description, hash, pdf_url, xml_url, cdr_url, last_error, created_at, updated_at`

func optional(v id.ID) any {
	if v.IsNil() {
		return nil
	}
	return v
}

func (r *Invoices) Create(ctx context.Context, inv *domain.Invoice) error {
	_, err := mysql.Conn(ctx, r.db).ExecContext(ctx, "INSERT INTO invoices ("+cols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inv.ID, inv.OrderID, inv.OrderNumber, string(inv.Type), inv.Series, inv.Number, optional(inv.RefInvoiceID),
		string(inv.Currency), inv.Total.Decimal(), string(inv.Status), inv.Attempts, inv.SunatDescription, inv.Hash,
		inv.PDFURL, inv.XMLURL, inv.CDRURL, inv.LastError, inv.CreatedAt, inv.UpdatedAt)
	if _, dup := mysql.DuplicateKey(err); dup {
		return apperr.Conflict("invoicing.duplicate", "order_id", "the order already has this document")
	}
	return err
}

func (r *Invoices) Update(ctx context.Context, inv *domain.Invoice) error {
	_, err := mysql.Conn(ctx, r.db).ExecContext(ctx, `UPDATE invoices SET status = ?, attempts = ?, sunat_description = ?, hash = ?,
		pdf_url = ?, xml_url = ?, cdr_url = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		string(inv.Status), inv.Attempts, trunc(inv.SunatDescription, 500), trunc(inv.Hash, 100), trunc(inv.PDFURL, 500),
		trunc(inv.XMLURL, 500), trunc(inv.CDRURL, 500), trunc(inv.LastError, 1000), inv.UpdatedAt, inv.ID)
	return err
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

type nullID struct{ ID id.ID }

func (n *nullID) Scan(v any) error {
	if v == nil {
		n.ID = id.Nil
		return nil
	}
	return n.ID.Scan(v)
}

type scanner interface{ Scan(dest ...any) error }

func scan(s scanner) (*domain.Invoice, error) {
	inv := &domain.Invoice{}
	var ref nullID
	var t, cur, total, status string
	if err := s.Scan(&inv.ID, &inv.OrderID, &inv.OrderNumber, &t, &inv.Series, &inv.Number, &ref, &cur, &total, &status, &inv.Attempts,
		&inv.SunatDescription, &inv.Hash, &inv.PDFURL, &inv.XMLURL, &inv.CDRURL, &inv.LastError, &inv.CreatedAt, &inv.UpdatedAt); err != nil {
		return nil, err
	}
	inv.Type, inv.Status, inv.Currency, inv.RefInvoiceID = domain.DocType(t), domain.Status(status), money.Currency(cur), ref.ID
	var err error
	inv.Total, err = money.Parse(total, inv.Currency)
	return inv, err
}

func (r *Invoices) query(ctx context.Context, q string, args ...any) ([]*domain.Invoice, error) {
	rows, err := mysql.Conn(ctx, r.db).QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.Invoice
	for rows.Next() {
		inv, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (r *Invoices) ByID(ctx context.Context, invoiceID id.ID) (*domain.Invoice, error) {
	inv, err := scan(mysql.Conn(ctx, r.db).QueryRowContext(ctx, "SELECT "+cols+" FROM invoices WHERE id = ?", invoiceID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("invoicing.not_found", "document not found")
	}
	return inv, err
}

func (r *Invoices) ByOrder(ctx context.Context, orderID id.ID) ([]*domain.Invoice, error) {
	return r.query(ctx, "SELECT "+cols+" FROM invoices WHERE order_id = ? ORDER BY created_at", orderID)
}

func (r *Invoices) List(ctx context.Context, f ports.Filter) ([]*domain.Invoice, int, error) {
	var where []string
	var args []any
	if f.Status != "" {
		where, args = append(where, "status = ?"), append(args, string(f.Status))
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := mysql.Conn(ctx, r.db).QueryRowContext(ctx, "SELECT COUNT(*) FROM invoices"+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	out, err := r.query(ctx, "SELECT "+cols+" FROM invoices"+cond+" ORDER BY created_at DESC LIMIT ? OFFSET ?", append(args, f.Limit, f.Offset)...)
	return out, total, err
}

func (r *Invoices) Stale(ctx context.Context, before time.Time, limit int) ([]*domain.Invoice, error) {
	return r.query(ctx, "SELECT "+cols+" FROM invoices WHERE status = 'pending' AND updated_at < ? ORDER BY updated_at LIMIT ?", before, limit)
}
