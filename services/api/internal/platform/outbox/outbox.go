// Package outbox records domain events in the same transaction as the state
// change that produced them (transactional outbox). The outbox-relay process
// (phase 5) publishes pending rows to SNS.
package outbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
)

// Event is the envelope every consumer receives.
type Event struct {
	ID            id.ID           `json:"id"`
	Type          string          `json:"type"`
	Version       int             `json:"version"`
	OccurredAt    time.Time       `json:"occurred_at"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   id.ID           `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
}

type Writer struct{ db *sql.DB }

func NewWriter(db *sql.DB) *Writer { return &Writer{db: db} }

// Record stores an event. Call it inside the unit of work that changes the
// aggregate, so the event exists if and only if the change committed.
func (w *Writer) Record(ctx context.Context, aggregateType string, aggregateID id.ID, eventType string, payload any, at time.Time) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("outbox payload: %w", err)
	}
	_, err = mysql.Conn(ctx, w.db).ExecContext(ctx,
		`INSERT INTO outbox (id, aggregate_type, aggregate_id, event_type, payload, occurred_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id.New(), aggregateType, aggregateID, eventType, body, at)
	return err
}
