// Package application files sheets of the Libro de Reclamaciones and records
// the store's answers. Both steps publish an event with the whole sheet so
// notifications can email the consumer a copy (ComplaintFiled) and the answer
// (ComplaintAnswered), and alert operations.
package application

import (
	"context"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/complaints/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/complaints/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

type Deps struct {
	Complaints ports.Complaints
	UoW        ports.UnitOfWork
	Events     ports.Events
	Now        func() time.Time
}

type Config struct {
	Provider domain.Provider // printed on every sheet
}

type Service struct {
	d   Deps
	cfg Config
}

func NewService(d Deps, cfg Config) *Service { return &Service{d: d, cfg: cfg} }

// Provider is the store's identification shown on the blank form.
func (s *Service) Provider() domain.Provider { return s.cfg.Provider }

// File numbers and stores a sheet. A retry with the same idempotency key
// returns the sheet filed the first time.
func (s *Service) File(ctx context.Context, in domain.Input, accountID id.ID, idempotencyKey string) (*domain.Complaint, error) {
	key := strings.TrimSpace(idempotencyKey)
	if len(key) > 64 {
		return nil, apperr.Invalid("complaints.idempotency_key_invalid", "Idempotency-Key", "use at most 64 characters")
	}
	if key != "" {
		if c, err := s.d.Complaints.ByIdempotencyKey(ctx, key); err == nil || !apperr.IsKind(err, apperr.KindNotFound) {
			return c, err
		}
	}
	c, err := domain.New(in, accountID, s.cfg.Provider, s.d.Now())
	if err != nil {
		return nil, err
	}
	err = s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		n, err := s.d.Complaints.NextNumber(ctx, domain.YearOf(c.FiledAt))
		if err != nil {
			return err
		}
		c.Assign(n)
		if err := s.d.Complaints.Create(ctx, c, key); err != nil {
			return err
		}
		return s.d.Events.Record(ctx, "complaint", c.ID, "ComplaintFiled", SheetOf(c), c.FiledAt)
	})
	if key != "" && apperr.IsKind(err, apperr.KindConflict) { // a concurrent retry won the race
		return s.d.Complaints.ByIdempotencyKey(ctx, key)
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Answer records the store's answer and emails it to the consumer.
func (s *Service) Answer(ctx context.Context, complaintID id.ID, response, by string) (*domain.Complaint, error) {
	if by = strings.TrimSpace(by); by == "" {
		by = "admin"
	}
	var c *domain.Complaint
	err := s.d.UoW.InTx(ctx, func(ctx context.Context) error {
		var err error
		if c, err = s.d.Complaints.ByID(ctx, complaintID); err != nil {
			return err
		}
		now := s.d.Now()
		if err := c.Answer(response, by, now); err != nil {
			return err
		}
		if err := s.d.Complaints.SaveAnswer(ctx, c); err != nil {
			return err
		}
		return s.d.Events.Record(ctx, "complaint", c.ID, "ComplaintAnswered", SheetOf(c), now)
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) Get(ctx context.Context, complaintID id.ID) (*domain.Complaint, error) {
	return s.d.Complaints.ByID(ctx, complaintID)
}

// Page is a page of sheets plus the counters the admin panel highlights.
type Page struct {
	Items   []*domain.Complaint
	Total   int
	Open    int
	Overdue int
}

func (s *Service) List(ctx context.Context, f ports.Filter) (Page, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	items, total, err := s.d.Complaints.List(ctx, f)
	if err != nil {
		return Page{}, err
	}
	open, overdue, err := s.d.Complaints.CountOpen(ctx, s.d.Now())
	return Page{Items: items, Total: total, Open: open, Overdue: overdue}, err
}

// Sheet is the payload of ComplaintFiled and ComplaintAnswered: the whole
// sheet, so the email copy needs nothing else. Notifications decodes it.
type Sheet struct {
	ComplaintID     id.ID         `json:"complaint_id"`
	Code            string        `json:"code"`
	Kind            domain.Kind   `json:"kind"`
	Locale          locale.Locale `json:"locale"`
	FiledAt         time.Time     `json:"filed_at"`
	DueAt           time.Time     `json:"due_at"`
	ProviderName    string        `json:"provider_name"`
	ProviderRUC     string        `json:"provider_ruc"`
	ProviderAddress string        `json:"provider_address"`
	Name            string        `json:"name"`
	DocType         string        `json:"document_type"`
	DocNumber       string        `json:"document_number"`
	Address         string        `json:"address"`
	Phone           string        `json:"phone"`
	Email           string        `json:"email"`
	Guardian        string        `json:"guardian"`
	ItemType        string        `json:"item_type"`
	OrderNumber     string        `json:"order_number"`
	Amount          string        `json:"amount"` // decimal in soles, empty if not given
	ItemDescription string        `json:"item_description"`
	Detail          string        `json:"detail"`
	Request         string        `json:"request"`
	Channel         string        `json:"response_channel"`
	Response        string        `json:"response"`
	AnsweredAt      *time.Time    `json:"answered_at"`
}

func SheetOf(c *domain.Complaint) Sheet {
	s := Sheet{
		ComplaintID: c.ID, Code: c.Code(), Kind: c.Kind, Locale: c.Locale, FiledAt: c.FiledAt, DueAt: c.DueAt,
		ProviderName: c.Provider.Name, ProviderRUC: c.Provider.RUC, ProviderAddress: c.Provider.Address,
		Name: c.Consumer.Name, DocType: c.Consumer.DocType, DocNumber: c.Consumer.DocNumber, Address: c.Consumer.Address,
		Phone: c.Consumer.Phone, Email: c.Consumer.Email, Guardian: c.Consumer.Guardian,
		ItemType: string(c.ItemType), OrderNumber: c.OrderNumber, ItemDescription: c.ItemDescription,
		Detail: c.Detail, Request: c.Request, Channel: string(c.Channel), Response: c.Response,
	}
	if c.Amount != nil {
		s.Amount = c.Amount.Decimal()
	}
	if c.Status == domain.StatusAnswered {
		at := c.AnsweredAt
		s.AnsweredAt = &at
	}
	return s
}
