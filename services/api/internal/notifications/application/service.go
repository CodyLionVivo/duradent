// Package application turns domain events into emails.
package application

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
	"github.com/CodyLionVivo/duradent/services/api/internal/notifications/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/notifications/ports"
)

type Deps struct {
	Orders   ports.Orders
	Accounts ports.Accounts
	Mailer   ports.Mailer
	Log      ports.EmailLog
	Logger   *slog.Logger
	Now      func() time.Time
}

type Config struct {
	WebBaseURL  string   // links in emails point here
	BankDetails string   // transfer instructions
	OpsEmails   []string // receive admin notices (stock, invoicing errors)
}

type Service struct {
	d   Deps
	cfg Config
}

func NewService(d Deps, cfg Config) *Service {
	cfg.WebBaseURL = strings.TrimRight(cfg.WebBaseURL, "/")
	return &Service{d: d, cfg: cfg}
}

// Events lists the event types this context consumes (SQS filter policy).
var Events = []string{
	"CustomerRegistered", "OrderPlaced", "PaymentConfirmed", "OrderShipped", "OrderCancelled",
	"OrderRefunded", "InvoiceIssued", "InvoiceRejected", "StockLow", "ComplaintFiled", "ComplaintAnswered",
}

type payload struct {
	OrderID      id.ID  `json:"order_id"`
	AccountID    id.ID  `json:"account_id"`
	Reason       string `json:"reason"`
	DocumentName string `json:"document_name"`
	PDFURL       string `json:"pdf_url"`
	Error        string `json:"error"`
	VariantID    id.ID  `json:"variant_id"`
	SKU          string `json:"sku"`
	Available    int    `json:"available"`
}

// Handle sends the emails an event calls for. Unknown events are ignored.
func (s *Service) Handle(ctx context.Context, e ports.Event) error {
	var p payload
	if len(e.Payload) > 0 {
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			return fmt.Errorf("payload of %s: %w", e.Type, err)
		}
	}
	switch e.Type {
	case "CustomerRegistered":
		r, err := s.d.Accounts.Recipient(ctx, e.AggregateID)
		if err != nil {
			return err
		}
		return s.send(ctx, e.ID, domain.KindWelcome, r, "", domain.Data{Name: r.Name, Link: s.link(r.Locale, "/account")})
	case "OrderPlaced":
		return s.orderEmail(ctx, e, p, domain.KindOrderPlaced)
	case "PaymentConfirmed":
		return s.orderEmail(ctx, e, p, domain.KindPaymentConfirmed)
	case "OrderShipped":
		return s.orderEmail(ctx, e, p, domain.KindOrderShipped)
	case "OrderCancelled":
		return s.orderEmail(ctx, e, p, domain.KindOrderCancelled)
	case "OrderRefunded":
		return s.orderEmail(ctx, e, p, domain.KindOrderRefunded)
	case "InvoiceIssued":
		return s.orderEmail(ctx, e, p, domain.KindInvoiceIssued)
	case "InvoiceRejected":
		o, err := s.d.Orders.Order(ctx, p.OrderID)
		if err != nil {
			return err
		}
		return s.ops(ctx, e.ID, domain.KindAdminInvoiceError, o.Number, domain.Data{
			OrderNumber: o.Number, Reason: p.Error, Link: s.cfg.WebBaseURL + "/admin/orders",
		})
	case "StockLow":
		sku := p.SKU
		if sku == "" {
			sku = p.VariantID.String()
		}
		return s.ops(ctx, e.ID, domain.KindAdminStockLow, "", domain.Data{
			SKU: sku, Available: p.Available, Link: s.cfg.WebBaseURL + "/admin/inventory",
		})
	case "ComplaintFiled", "ComplaintAnswered":
		return s.complaint(ctx, e)
	}
	return nil
}

// sheet is the payload of the complaints events (complaints/application.Sheet).
type sheet struct {
	ComplaintID     string     `json:"complaint_id"`
	Code            string     `json:"code"`
	Kind            string     `json:"kind"`
	Locale          string     `json:"locale"`
	FiledAt         time.Time  `json:"filed_at"`
	DueAt           time.Time  `json:"due_at"`
	ProviderName    string     `json:"provider_name"`
	ProviderRUC     string     `json:"provider_ruc"`
	ProviderAddress string     `json:"provider_address"`
	Name            string     `json:"name"`
	DocType         string     `json:"document_type"`
	DocNumber       string     `json:"document_number"`
	Address         string     `json:"address"`
	Phone           string     `json:"phone"`
	Email           string     `json:"email"`
	Guardian        string     `json:"guardian"`
	ItemType        string     `json:"item_type"`
	OrderNumber     string     `json:"order_number"`
	Amount          string     `json:"amount"`
	ItemDescription string     `json:"item_description"`
	Detail          string     `json:"detail"`
	Request         string     `json:"request"`
	Channel         string     `json:"response_channel"`
	Response        string     `json:"response"`
	AnsweredAt      *time.Time `json:"answered_at"`
}

// complaint emails the consumer their copy of the sheet (or the answer) and,
// for a new sheet, alerts operations: the 15-day deadline starts now.
func (s *Service) complaint(ctx context.Context, e ports.Event) error {
	var p sheet
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		return fmt.Errorf("payload of %s: %w", e.Type, err)
	}
	c := domain.Complaint{
		ID: p.ComplaintID, Code: p.Code, Kind: p.Kind, Locale: locale.ParseOrDefault(p.Locale), FiledAt: p.FiledAt, DueAt: p.DueAt,
		ProviderName: p.ProviderName, ProviderRUC: p.ProviderRUC, ProviderAddress: p.ProviderAddress,
		Name: p.Name, DocType: p.DocType, DocNumber: p.DocNumber, Address: p.Address, Phone: p.Phone, Email: p.Email,
		Guardian: p.Guardian, ItemType: p.ItemType, OrderNumber: p.OrderNumber, ItemDescription: p.ItemDescription,
		Detail: p.Detail, Request: p.Request, Channel: p.Channel, Response: p.Response, AnsweredAt: p.AnsweredAt,
	}
	if p.Amount != "" {
		m, err := money.Parse(p.Amount, money.PEN)
		if err != nil {
			return fmt.Errorf("amount of complaint %s: %w", p.Code, err)
		}
		c.Amount = &m
	}
	data := func(l locale.Locale) domain.Data {
		return domain.Data{
			Name: c.Name, Code: c.Code, ComplaintKind: domain.ComplaintKind(c.Kind, l), Response: c.Response,
			Deadline: domain.FormatDate(c.DueAt, l), Sheet: domain.ComplaintRows(c, l),
		}
	}
	kind := domain.KindComplaintFiled
	if e.Type == "ComplaintAnswered" {
		kind = domain.KindComplaintAnswered
	}
	r := ports.Recipient{Email: c.Email, Name: c.Name, Locale: c.Locale}
	if err := s.send(ctx, e.ID, kind, r, c.OrderNumber, data(c.Locale)); err != nil || kind != domain.KindComplaintFiled {
		return err
	}
	d := data(locale.ES)
	d.Link = s.cfg.WebBaseURL + "/admin/complaints/" + c.ID
	return s.ops(ctx, e.ID, domain.KindAdminComplaintFiled, c.OrderNumber, d)
}

func (s *Service) orderEmail(ctx context.Context, e ports.Event, p payload, kind domain.Kind) error {
	orderID := p.OrderID
	if orderID.IsNil() {
		orderID = e.AggregateID
	}
	o, err := s.d.Orders.Order(ctx, orderID)
	if err != nil {
		return err
	}
	r, err := s.d.Accounts.Recipient(ctx, o.AccountID)
	if err != nil {
		return err
	}
	if o.Email != "" {
		r.Email = o.Email // the address the order was placed with
	}
	d := domain.Data{
		Name: r.Name, OrderNumber: o.Number, Total: domain.FormatMoney(o.Total), Method: o.Method,
		Deadline: domain.FormatDeadline(o.PaymentDeadline, r.Locale), Carrier: o.Carrier, Tracking: o.Tracking,
		Reason: o.CancelReason, Link: s.link(r.Locale, "/orders/"+o.Number),
	}
	if o.Method == "bank_transfer" {
		d.BankDetails = s.cfg.BankDetails
	}
	switch kind {
	case domain.KindOrderCancelled:
		if p.Reason != "" {
			d.Reason = p.Reason
		}
	case domain.KindInvoiceIssued:
		d.DocumentName, d.Link = p.DocumentName, p.PDFURL
	case domain.KindOrderPlaced, domain.KindPaymentConfirmed, domain.KindOrderShipped, domain.KindOrderRefunded:
	}
	return s.send(ctx, e.ID, kind, r, o.Number, d)
}

func (s *Service) ops(ctx context.Context, eventID id.ID, kind domain.Kind, orderNumber string, d domain.Data) error {
	for _, to := range s.cfg.OpsEmails {
		if err := s.send(ctx, eventID, kind, ports.Recipient{Email: to, Locale: locale.ES}, orderNumber, d); err != nil {
			return err
		}
	}
	return nil
}

// SendVerification emails a verification link right away (not through the
// bus: the token must not travel in event payloads).
func (s *Service) SendVerification(ctx context.Context, r ports.Recipient, token string) error {
	link := s.link(r.Locale, "/account/verify?token="+token)
	return s.send(ctx, id.New(), domain.KindVerifyEmail, r, "", domain.Data{Name: r.Name, Link: link})
}

// SendPasswordReset emails a link to create a new password, right away and
// for the same reason as SendVerification.
func (s *Service) SendPasswordReset(ctx context.Context, r ports.Recipient, token string) error {
	link := s.link(r.Locale, "/account/reset?token="+token)
	return s.send(ctx, id.New(), domain.KindPasswordReset, r, "", domain.Data{Name: r.Name, Link: link})
}

func (s *Service) link(l locale.Locale, path string) string {
	return s.cfg.WebBaseURL + "/" + string(l) + path
}

// send renders and delivers one email at most once per (event, kind, recipient).
func (s *Service) send(ctx context.Context, eventID id.ID, kind domain.Kind, r ports.Recipient, orderNumber string, d domain.Data) error {
	if r.Email == "" {
		s.d.Logger.WarnContext(ctx, "email skipped: no recipient", "kind", kind)
		return nil
	}
	d.LogoURL = s.cfg.WebBaseURL + "/brand/logo-email.png"
	m, err := domain.Render(kind, r.Locale, r.Email, d)
	if err != nil {
		return err
	}
	entry := ports.LogEntry{EventID: eventID, Kind: kind, To: r.Email, Subject: m.Subject, OrderNumber: orderNumber}
	sent, err := s.d.Log.Begin(ctx, entry, s.d.Now())
	if err != nil || sent {
		return err
	}
	providerID, err := s.d.Mailer.Send(ctx, m)
	if err != nil {
		if lerr := s.d.Log.Failed(ctx, entry, err.Error()); lerr != nil {
			s.d.Logger.ErrorContext(ctx, "email log", "error", lerr)
		}
		return fmt.Errorf("send %s: %w", kind, err)
	}
	return s.d.Log.Sent(ctx, entry, providerID, s.d.Now())
}
