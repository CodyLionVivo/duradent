// Package ports declares what the notifications context needs: the events it
// reacts to, read access to orders and accounts, a mail transport and a log
// of what was sent.
package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/notifications/domain"
)

// Event is a domain event delivered by the worker.
type Event struct {
	ID          id.ID
	Type        string
	AggregateID id.ID
	Payload     json.RawMessage
}

type Recipient struct {
	Email  string
	Name   string
	Locale locale.Locale
}

type OrderInfo struct {
	Number          string
	AccountID       id.ID
	Email           string
	Total           money.Money
	Method          string
	PaymentDeadline time.Time
	Carrier         string
	Tracking        string
	CancelReason    string
}

type Orders interface {
	Order(ctx context.Context, orderID id.ID) (OrderInfo, error)
}

type Accounts interface {
	Recipient(ctx context.Context, accountID id.ID) (Recipient, error)
}

// Mailer delivers a message and returns the provider's message id.
type Mailer interface {
	Send(ctx context.Context, m domain.Message) (string, error)
}

// LogEntry identifies one email caused by one event.
type LogEntry struct {
	EventID     id.ID
	Kind        domain.Kind
	To          string
	Subject     string
	OrderNumber string
}

type EmailLog interface {
	// Begin records the attempt; sent=true when this email already went out.
	Begin(ctx context.Context, e LogEntry, at time.Time) (sent bool, err error)
	Sent(ctx context.Context, e LogEntry, providerID string, at time.Time) error
	Failed(ctx context.Context, e LogEntry, reason string) error
}
