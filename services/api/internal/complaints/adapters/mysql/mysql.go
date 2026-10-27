// Package mysql stores the sheets of the Libro de Reclamaciones and their
// yearly counters.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/complaints/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/complaints/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
)

type Complaints struct{ db *sql.DB }

func New(db *sql.DB) *Complaints { return &Complaints{db: db} }

var _ ports.Complaints = (*Complaints)(nil)

// NextNumber increments the year's row; the row lock holds until the
// transaction ends, so sheets are numbered without gaps or repeats.
func (r *Complaints) NextNumber(ctx context.Context, year int) (int, error) {
	q := mysql.Conn(ctx, r.db)
	if _, err := q.ExecContext(ctx, `INSERT INTO complaint_sequences (year, last_number) VALUES (?, LAST_INSERT_ID(1))
		ON DUPLICATE KEY UPDATE last_number = LAST_INSERT_ID(last_number + 1)`, year); err != nil {
		return 0, err
	}
	var n int
	err := q.QueryRowContext(ctx, "SELECT LAST_INSERT_ID()").Scan(&n)
	return n, err
}

const cols = `id, year, number, kind, account_id, consumer_name, document_type, document_number, address, phone, email,
	guardian, item_type, order_number, currency, amount, item_description, detail, request, response_channel, locale,
	provider_name, provider_ruc, provider_address, status, response, answered_by, answered_at, filed_at, due_at, updated_at`

func nullable[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}

func (r *Complaints) Create(ctx context.Context, c *domain.Complaint, idempotencyKey string) error {
	var amount any
	if c.Amount != nil {
		amount = c.Amount.Decimal()
	}
	var accountID any
	if !c.AccountID.IsNil() {
		accountID = c.AccountID
	}
	_, err := mysql.Conn(ctx, r.db).ExecContext(ctx, "INSERT INTO complaints ("+cols+`, idempotency_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Year, c.Number, string(c.Kind), accountID, c.Consumer.Name, c.Consumer.DocType, c.Consumer.DocNumber,
		c.Consumer.Address, c.Consumer.Phone, c.Consumer.Email, c.Consumer.Guardian, string(c.ItemType), c.OrderNumber,
		string(money.PEN), amount, c.ItemDescription, c.Detail, c.Request, string(c.Channel), string(c.Locale),
		c.Provider.Name, c.Provider.RUC, c.Provider.Address, string(c.Status), c.Response, c.AnsweredBy,
		nullable(c.AnsweredAt), c.FiledAt, c.DueAt, c.UpdatedAt, nullable(idempotencyKey))
	if _, dup := mysql.DuplicateKey(err); dup {
		return apperr.Conflict("complaints.duplicate", "Idempotency-Key", "this sheet was already filed")
	}
	return err
}

func (r *Complaints) SaveAnswer(ctx context.Context, c *domain.Complaint) error {
	res, err := mysql.Conn(ctx, r.db).ExecContext(ctx, `UPDATE complaints SET status = ?, response = ?, answered_by = ?,
		answered_at = ?, updated_at = ? WHERE id = ? AND status = 'open'`,
		string(c.Status), c.Response, c.AnsweredBy, c.AnsweredAt, c.UpdatedAt, c.ID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errors.Join(err, apperr.Conflict("complaints.already_answered", "status", "this sheet was already answered"))
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scan(s scanner) (*domain.Complaint, error) {
	c := &domain.Complaint{}
	var account []byte
	var kind, item, cur, channel, loc, status string
	var amount sql.NullString
	var answeredAt sql.NullTime
	if err := s.Scan(&c.ID, &c.Year, &c.Number, &kind, &account, &c.Consumer.Name, &c.Consumer.DocType, &c.Consumer.DocNumber,
		&c.Consumer.Address, &c.Consumer.Phone, &c.Consumer.Email, &c.Consumer.Guardian, &item, &c.OrderNumber, &cur, &amount,
		&c.ItemDescription, &c.Detail, &c.Request, &channel, &loc, &c.Provider.Name, &c.Provider.RUC, &c.Provider.Address,
		&status, &c.Response, &c.AnsweredBy, &answeredAt, &c.FiledAt, &c.DueAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Kind, c.ItemType, c.Channel, c.Status = domain.Kind(kind), domain.ItemType(item), domain.Channel(channel), domain.Status(status)
	c.Locale = locale.ParseOrDefault(loc)
	if account != nil {
		if err := c.AccountID.Scan(account); err != nil {
			return nil, err
		}
	}
	if amount.Valid {
		m, err := money.Parse(amount.String, money.Currency(cur))
		if err != nil {
			return nil, err
		}
		c.Amount = &m
	}
	if answeredAt.Valid {
		c.AnsweredAt = answeredAt.Time
	}
	return c, nil
}

func (r *Complaints) one(ctx context.Context, where string, arg any) (*domain.Complaint, error) {
	c, err := scan(mysql.Conn(ctx, r.db).QueryRowContext(ctx, "SELECT "+cols+" FROM complaints WHERE "+where, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperr.NotFound("complaints.not_found", "complaint not found")
	}
	return c, err
}

func (r *Complaints) ByID(ctx context.Context, complaintID id.ID) (*domain.Complaint, error) {
	return r.one(ctx, "id = ?", complaintID)
}

func (r *Complaints) ByIdempotencyKey(ctx context.Context, key string) (*domain.Complaint, error) {
	return r.one(ctx, "idempotency_key = ?", key)
}

// code matches a sheet number as printed ("12-2026" or "000012-2026").
func code(q string) (year, number int, ok bool) {
	n, y, found := strings.Cut(q, "-")
	number, err1 := strconv.Atoi(n)
	year, err2 := strconv.Atoi(y)
	return year, number, found && err1 == nil && err2 == nil && len(y) == 4
}

func (r *Complaints) List(ctx context.Context, f ports.Filter) ([]*domain.Complaint, int, error) {
	var where []string
	var args []any
	if f.Status != "" {
		where, args = append(where, "status = ?"), append(args, string(f.Status))
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		if year, number, ok := code(q); ok {
			where, args = append(where, "year = ? AND number = ?"), append(args, year, number)
		} else {
			like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(q) + "%"
			where = append(where, "(consumer_name LIKE ? OR email LIKE ? OR document_number LIKE ? OR order_number LIKE ?)")
			args = append(args, like, like, like, like)
		}
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	q := mysql.Conn(ctx, r.db)
	var total int
	if err := q.QueryRowContext(ctx, "SELECT COUNT(*) FROM complaints"+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	// Open sheets first, the most urgent on top; then the answered ones, newest first.
	rows, err := q.QueryContext(ctx, "SELECT "+cols+" FROM complaints"+cond+
		" ORDER BY status = 'answered', CASE WHEN status = 'open' THEN due_at END, filed_at DESC LIMIT ? OFFSET ?",
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.Complaint
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

func (r *Complaints) CountOpen(ctx context.Context, now time.Time) (open, overdue int, err error) {
	err = mysql.Conn(ctx, r.db).QueryRowContext(ctx,
		"SELECT COUNT(*), COALESCE(SUM(due_at < ?), 0) FROM complaints WHERE status = 'open'", now).Scan(&open, &overdue)
	return open, overdue, err
}
