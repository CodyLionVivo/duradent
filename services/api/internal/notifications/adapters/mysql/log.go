// Package mysql stores the email log.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/notifications/ports"
	platformmysql "github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
)

type Log struct{ db *sql.DB }

func New(db *sql.DB) *Log { return &Log{db: db} }

func (l *Log) Begin(ctx context.Context, e ports.LogEntry, at time.Time) (bool, error) {
	conn := platformmysql.Conn(ctx, l.db)
	var status string
	err := conn.QueryRowContext(ctx, "SELECT status FROM email_log WHERE event_id = ? AND kind = ? AND recipient = ?",
		e.EventID, string(e.Kind), e.To).Scan(&status)
	switch {
	case err == nil:
		return status == "sent", nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
	_, err = conn.ExecContext(ctx, `INSERT IGNORE INTO email_log (id, event_id, kind, recipient, subject, order_number, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'sending', ?)`, id.New(), e.EventID, string(e.Kind), e.To, truncate(e.Subject, 300), e.OrderNumber, at)
	return false, err
}

func (l *Log) Sent(ctx context.Context, e ports.LogEntry, providerID string, at time.Time) error {
	_, err := platformmysql.Conn(ctx, l.db).ExecContext(ctx,
		"UPDATE email_log SET status = 'sent', provider_id = ?, error = '', sent_at = ? WHERE event_id = ? AND kind = ? AND recipient = ?",
		truncate(providerID, 200), at, e.EventID, string(e.Kind), e.To)
	return err
}

func (l *Log) Failed(ctx context.Context, e ports.LogEntry, reason string) error {
	_, err := platformmysql.Conn(ctx, l.db).ExecContext(ctx,
		"UPDATE email_log SET status = 'failed', error = ? WHERE event_id = ? AND kind = ? AND recipient = ?",
		truncate(reason, 1000), e.EventID, string(e.Kind), e.To)
	return err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
