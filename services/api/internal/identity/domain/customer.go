package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

type CustomerType string

const (
	CustomerPerson  CustomerType = "persona" // individual practitioner: boleta, or factura with RUC 10
	CustomerCompany CustomerType = "empresa" // clinic or company: factura with RUC 20
)

type DocumentType string

const (
	DocDNI DocumentType = "DNI" // 8 digits
	DocRUC DocumentType = "RUC" // 11 digits with check digit
	DocCE  DocumentType = "CE"  // carné de extranjería
)

// Customer is the billing profile of an account. Phase 4 invoices from it:
// factura (01) when the document is a RUC, boleta (03) otherwise.
type Customer struct {
	AccountID      id.ID
	Type           CustomerType
	DocumentType   DocumentType
	DocumentNumber string
	BusinessName   string // razón social; required for companies
	COPNumber      string // Colegio Odontológico del Perú registration, optional
	Phone          string
	PriceListCode  string // assigned by an admin; empty means the public list
	UpdatedAt      time.Time
}

type CustomerInput struct {
	Type           CustomerType
	DocumentType   DocumentType
	DocumentNumber string
	BusinessName   string
	COPNumber      string
	Phone          string
}

var (
	digitsRe = regexp.MustCompile(`^\d+$`)
	ceRe     = regexp.MustCompile(`^[A-Z0-9]{6,12}$`)
	copRe    = regexp.MustCompile(`^\d{1,6}$`)
	phoneRe  = regexp.MustCompile(`^\+?[0-9 ]{6,20}$`)
)

// ValidRUC checks length, prefix and the SUNAT modulo-11 check digit.
func ValidRUC(ruc string) bool {
	if len(ruc) != 11 || !digitsRe.MatchString(ruc) {
		return false
	}
	switch ruc[:2] {
	case "10", "15", "16", "17", "20":
	default:
		return false
	}
	weights := []int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
	sum := 0
	for i, w := range weights {
		sum += int(ruc[i]-'0') * w
	}
	check := 11 - sum%11
	switch check {
	case 10:
		check = 0
	case 11:
		check = 1
	}
	return int(ruc[10]-'0') == check
}

// NewCustomer validates a billing profile. Companies must invoice with a RUC
// starting with 20 and a business name.
func NewCustomer(accountID id.ID, in CustomerInput, now time.Time) (*Customer, error) {
	c := &Customer{AccountID: accountID}
	if err := c.Update(in, now); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Customer) Update(in CustomerInput, now time.Time) error {
	doc := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(in.DocumentNumber), " ", ""))
	name := strings.TrimSpace(in.BusinessName)
	cop := strings.TrimSpace(in.COPNumber)
	phone := strings.TrimSpace(in.Phone)

	switch in.Type {
	case CustomerPerson, CustomerCompany:
	default:
		return apperr.Invalid("identity.customer_type_invalid", "type", "choose persona or empresa")
	}
	switch in.DocumentType {
	case DocDNI:
		if len(doc) != 8 || !digitsRe.MatchString(doc) {
			return apperr.Invalid("identity.dni_invalid", "document_number", "a DNI has 8 digits")
		}
	case DocRUC:
		if !ValidRUC(doc) {
			return apperr.Invalid("identity.ruc_invalid", "document_number", "invalid RUC")
		}
	case DocCE:
		if !ceRe.MatchString(doc) {
			return apperr.Invalid("identity.ce_invalid", "document_number", "invalid carné de extranjería")
		}
	default:
		return apperr.Invalid("identity.document_type_invalid", "document_type", "choose DNI, RUC or CE")
	}
	if in.Type == CustomerCompany {
		if in.DocumentType != DocRUC || !strings.HasPrefix(doc, "20") {
			return apperr.Invalid("identity.company_needs_ruc20", "document_number", "a company needs a RUC starting with 20")
		}
		if name == "" {
			return apperr.Invalid("identity.business_name_required", "business_name", "business name is required for companies")
		}
	}
	if utf8.RuneCountInString(name) > 200 {
		return apperr.Invalid("identity.business_name_too_long", "business_name", "business name is too long")
	}
	if cop != "" && !copRe.MatchString(cop) {
		return apperr.Invalid("identity.cop_invalid", "cop_number", "COP number has up to 6 digits")
	}
	if phone != "" && !phoneRe.MatchString(phone) {
		return apperr.Invalid("identity.phone_invalid", "phone", "invalid phone number")
	}
	c.Type, c.DocumentType, c.DocumentNumber = in.Type, in.DocumentType, doc
	c.BusinessName, c.COPNumber, c.Phone = name, cop, phone
	c.UpdatedAt = now
	return nil
}

// InvoiceType returns the SUNAT document the customer receives: factura (01)
// with a RUC, boleta (03) otherwise.
func (c *Customer) InvoiceType() string {
	if c.DocumentType == DocRUC {
		return "01"
	}
	return "03"
}

func (c *Customer) AssignPriceList(code string, now time.Time) {
	c.PriceListCode = strings.TrimSpace(code)
	c.UpdatedAt = now
}
