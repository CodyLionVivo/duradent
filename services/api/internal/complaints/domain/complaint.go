// Package domain models the Libro de Reclamaciones virtual that Peruvian law
// (Código de Protección y Defensa del Consumidor, Ley 29571, and its
// reglamento) requires of every store that sells online: numbered sheets
// ("hojas de reclamación"), a copy for the consumer and an answer within 15
// business days.
package domain

import (
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/money"
)

// ResponseDays is the legal deadline to answer, in business days.
const ResponseDays = 15

// lima is Peru's time zone (no daylight saving): sheets are numbered per
// calendar year and deadlines counted in Lima.
var lima = time.FixedZone("PET", -5*60*60)

type Kind string

const (
	KindClaim     Kind = "reclamo" // disagreement with the products or services
	KindComplaint Kind = "queja"   // not about them: attention, treatment, service
)

type ItemType string

const (
	ItemProduct ItemType = "producto"
	ItemService ItemType = "servicio"
)

// Channel is how the consumer wants the answer.
type Channel string

const (
	ChannelEmail   Channel = "email"
	ChannelAddress Channel = "address" // a letter to their address
)

type Status string

const (
	StatusOpen     Status = "open"
	StatusAnswered Status = "answered"
)

// Provider identifies the store on the sheet, as it was when it was filed.
type Provider struct {
	Name    string
	RUC     string
	Address string
}

type Consumer struct {
	Name      string
	DocType   string // DNI, CE, RUC or PAS
	DocNumber string
	Address   string
	Phone     string
	Email     string
	Guardian  string // parent or guardian when the consumer is a minor
}

type Complaint struct {
	ID              id.ID
	Year            int
	Number          int // correlative within the year
	Kind            Kind
	AccountID       id.ID // signed-in account that filed it, if any
	Consumer        Consumer
	ItemType        ItemType
	OrderNumber     string
	Amount          *money.Money // amount claimed, optional
	ItemDescription string
	Detail          string
	Request         string // what the consumer asks for ("pedido")
	Channel         Channel
	Locale          locale.Locale
	Provider        Provider
	Status          Status
	Response        string
	AnsweredBy      string
	AnsweredAt      time.Time
	FiledAt         time.Time
	DueAt           time.Time
	UpdatedAt       time.Time
}

// Code is the sheet number as printed: "000001-2026".
func (c *Complaint) Code() string { return Code(c.Year, c.Number) }

func Code(year, number int) string { return fmt.Sprintf("%06d-%d", number, year) }

// Overdue reports whether the answer is late.
func (c *Complaint) Overdue(now time.Time) bool { return c.Status == StatusOpen && now.After(c.DueAt) }

// Input is what the consumer writes on the form.
type Input struct {
	Kind            string
	Name            string
	DocType         string
	DocNumber       string
	Address         string
	Phone           string
	Email           string
	Guardian        string
	ItemType        string
	OrderNumber     string
	Amount          string // decimal in soles, optional
	ItemDescription string
	Detail          string
	Request         string
	Channel         string
	Locale          locale.Locale
}

// New validates a sheet. The number is assigned when it is stored.
func New(in Input, accountID id.ID, p Provider, now time.Time) (*Complaint, error) {
	c := &Complaint{
		ID: id.New(), Kind: Kind(strings.TrimSpace(in.Kind)), AccountID: accountID,
		ItemType: ItemType(strings.TrimSpace(in.ItemType)), Channel: Channel(strings.TrimSpace(in.Channel)),
		Locale: in.Locale, Provider: p, Status: StatusOpen, FiledAt: now, DueAt: DueDate(now), UpdatedAt: now,
	}
	if c.Locale == "" {
		c.Locale = locale.Default
	}
	if c.Kind != KindClaim && c.Kind != KindComplaint {
		return nil, apperr.Invalid("complaints.kind_invalid", "kind", "choose reclamo or queja")
	}
	var err error
	if c.Consumer, err = consumer(in); err != nil {
		return nil, err
	}
	if c.ItemType != ItemProduct && c.ItemType != ItemService {
		return nil, apperr.Invalid("complaints.item_type_invalid", "item_type", "choose producto or servicio")
	}
	if c.OrderNumber = strings.ToUpper(strings.TrimSpace(in.OrderNumber)); utf8.RuneCountInString(c.OrderNumber) > 20 {
		return nil, apperr.Invalid("complaints.order_number_invalid", "order_number", "order number too long")
	}
	if a := strings.TrimSpace(in.Amount); a != "" {
		m, err := money.Parse(strings.ReplaceAll(a, ",", ""), money.PEN)
		if err != nil || m.Minor < 0 {
			return nil, apperr.Invalid("complaints.amount_invalid", "amount", "the amount must be a positive number in soles")
		}
		c.Amount = &m
	}
	texts := []struct {
		dst        *string
		val, field string
		max        int
	}{
		{&c.ItemDescription, in.ItemDescription, "item_description", 500},
		{&c.Detail, in.Detail, "detail", 3000},
		{&c.Request, in.Request, "request", 1000},
	}
	for _, f := range texts {
		v := strings.TrimSpace(f.val)
		if v == "" || utf8.RuneCountInString(v) > f.max {
			return nil, apperr.Invalid("complaints."+f.field+"_required", f.field, fmt.Sprintf("%s is required (up to %d characters)", f.field, f.max))
		}
		*f.dst = v
	}
	if c.Channel != ChannelEmail && c.Channel != ChannelAddress {
		return nil, apperr.Invalid("complaints.channel_invalid", "response_channel", "choose email or address")
	}
	return c, nil
}

func consumer(in Input) (Consumer, error) {
	c := Consumer{
		Name: strings.TrimSpace(in.Name), DocType: strings.ToUpper(strings.TrimSpace(in.DocType)),
		DocNumber: strings.ToUpper(strings.TrimSpace(in.DocNumber)), Address: strings.TrimSpace(in.Address),
		Phone: strings.TrimSpace(in.Phone), Email: strings.ToLower(strings.TrimSpace(in.Email)), Guardian: strings.TrimSpace(in.Guardian),
	}
	if c.Name == "" || utf8.RuneCountInString(c.Name) > 120 {
		return c, apperr.Invalid("complaints.name_required", "name", "enter your full name")
	}
	if !validDocument(c.DocType, c.DocNumber) {
		return c, apperr.Invalid("complaints.document_invalid", "document_number", "enter a valid DNI (8 digits), CE, RUC (11 digits) or passport")
	}
	if c.Address == "" || utf8.RuneCountInString(c.Address) > 200 {
		return c, apperr.Invalid("complaints.address_required", "address", "enter your address")
	}
	if addr, err := mail.ParseAddress(c.Email); err != nil || addr.Address != c.Email || len(c.Email) > 254 ||
		!strings.Contains(c.Email[strings.LastIndex(c.Email, "@")+1:], ".") {
		return c, apperr.Invalid("complaints.email_invalid", "email", "invalid email address")
	}
	if len(c.Phone) > 30 || strings.Trim(c.Phone, "+0123456789 -()") != "" {
		return c, apperr.Invalid("complaints.phone_invalid", "phone", "invalid phone number")
	}
	if utf8.RuneCountInString(c.Guardian) > 120 {
		return c, apperr.Invalid("complaints.guardian_invalid", "guardian", "name too long")
	}
	return c, nil
}

func validDocument(t, n string) bool {
	alnum := func(min, max int) bool {
		if len(n) < min || len(n) > max {
			return false
		}
		for _, r := range n {
			if (r < '0' || r > '9') && (r < 'A' || r > 'Z') {
				return false
			}
		}
		return true
	}
	digits := func(size int) bool { return len(n) == size && strings.Trim(n, "0123456789") == "" }
	switch t {
	case "DNI":
		return digits(8)
	case "RUC":
		return digits(11)
	case "CE":
		return alnum(8, 12)
	case "PAS":
		return alnum(6, 12)
	}
	return false
}

// Assign gives the sheet its correlative of the year it was filed in.
func (c *Complaint) Assign(number int) { c.Year, c.Number = YearOf(c.FiledAt), number }

// YearOf is the calendar year in Lima.
func YearOf(t time.Time) int { return t.In(lima).Year() }

// Answer records the provider's answer ("observaciones y acciones adoptadas").
func (c *Complaint) Answer(response, by string, now time.Time) error {
	if c.Status != StatusOpen {
		return apperr.Conflict("complaints.already_answered", "status", "this sheet was already answered")
	}
	response = strings.TrimSpace(response)
	if response == "" || utf8.RuneCountInString(response) > 5000 {
		return apperr.Invalid("complaints.response_required", "response", "write the answer (up to 5000 characters)")
	}
	c.Status, c.Response, c.AnsweredBy, c.AnsweredAt, c.UpdatedAt = StatusAnswered, response, by, now, now
	return nil
}

// DueDate is the end (Lima time) of the 15th business day after filing.
// Only weekends are skipped: ignoring public holidays makes the deadline
// earlier, never later, than the legal one.
func DueDate(filed time.Time) time.Time {
	d := filed.In(lima)
	day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, lima)
	for n := 0; n < ResponseDays; {
		day = day.AddDate(0, 0, 1)
		if wd := day.Weekday(); wd != time.Saturday && wd != time.Sunday {
			n++
		}
	}
	return day.Add(24*time.Hour - time.Microsecond).UTC()
}
