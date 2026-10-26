package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
)

// Publisher sends an event to the bus (SNS in AWS, LocalStack locally).
type Publisher interface {
	Publish(ctx context.Context, e Event) error
}

var (
	publishedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "outbox_published_total", Help: "Events published from the outbox, by result.",
	}, []string{"result"})
	pendingGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "outbox_pending", Help: "Events waiting in the outbox.",
	})
)

func init() { prometheus.MustRegister(publishedTotal, pendingGauge) }

// Relay publishes outbox rows. Delivery is at least once: an event may be
// published twice if the process dies between publishing and marking the
// row, which is why consumers are idempotent.
type Relay struct {
	db       *sql.DB
	pub      Publisher
	log      *slog.Logger
	batch    int
	interval time.Duration
}

func NewRelay(db *sql.DB, pub Publisher, log *slog.Logger) *Relay {
	return &Relay{db: db, pub: pub, log: log, batch: 50, interval: time.Second}
}

// Run polls until ctx is cancelled.
func (r *Relay) Run(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		n, err := r.PublishBatch(ctx)
		if err != nil {
			r.log.ErrorContext(ctx, "outbox relay", "error", err)
		}
		wait := r.interval
		if n == r.batch {
			wait = 0 // more waiting: keep going
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.updatePending(ctx)
		case <-time.After(wait):
		}
	}
}

func (r *Relay) updatePending(ctx context.Context) {
	var n float64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM outbox WHERE published_at IS NULL").Scan(&n); err == nil {
		pendingGauge.Set(n)
	}
}

// backoff grows exponentially up to ten minutes.
func backoff(attempts int) time.Duration {
	d := time.Second << min(attempts, 10)
	return min(d, 10*time.Minute)
}

type pendingRow struct {
	ev       Event
	attempts int
}

// pending locks the next rows to publish.
func pending(ctx context.Context, tx *sql.Tx, now time.Time, limit int) ([]pendingRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, aggregate_type, aggregate_id, event_type, payload, occurred_at, attempts FROM outbox
		WHERE published_at IS NULL AND (locked_until IS NULL OR locked_until <= ?)
		ORDER BY occurred_at, id LIMIT ? FOR UPDATE SKIP LOCKED`, now, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var batch []pendingRow
	for rows.Next() {
		var x pendingRow
		var payload []byte
		if err := rows.Scan(&x.ev.ID, &x.ev.AggregateType, &x.ev.AggregateID, &x.ev.Type, &payload, &x.ev.OccurredAt, &x.attempts); err != nil {
			return nil, err
		}
		x.ev.Version = 1
		x.ev.Payload = payload
		batch = append(batch, x)
	}
	return batch, rows.Err()
}

// PublishBatch publishes up to one batch and reports how many rows it took.
// SKIP LOCKED lets several relay tasks run without publishing the same row.
func (r *Relay) PublishBatch(ctx context.Context) (int, error) {
	var count int
	err := mysql.InTx(ctx, r.db, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		batch, err := pending(ctx, tx, now, r.batch)
		if err != nil {
			return err
		}
		count = len(batch)
		for _, x := range batch {
			if err := r.pub.Publish(ctx, x.ev); err != nil {
				publishedTotal.WithLabelValues("error").Inc()
				r.log.WarnContext(ctx, "publish event", "event", x.ev.Type, "id", x.ev.ID, "attempt", x.attempts+1, "error", err)
				msg := err.Error()
				if len(msg) > 1000 {
					msg = msg[:1000]
				}
				if _, err := tx.ExecContext(ctx, "UPDATE outbox SET attempts = attempts + 1, last_error = ?, locked_until = ? WHERE id = ?",
					msg, now.Add(backoff(x.attempts+1)), x.ev.ID); err != nil {
					return err
				}
				continue
			}
			publishedTotal.WithLabelValues("ok").Inc()
			if _, err := tx.ExecContext(ctx, "UPDATE outbox SET published_at = ?, attempts = attempts + 1, last_error = NULL WHERE id = ?", now, x.ev.ID); err != nil {
				return fmt.Errorf("mark published: %w", err)
			}
		}
		return nil
	})
	return count, err
}
