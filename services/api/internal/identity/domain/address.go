package domain

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

// Departments of Peru with their INEI codes (first two digits of an ubigeo).
// Shipping zones (phase 4) are defined per department.
var Departments = map[string]string{
	"01": "Amazonas", "02": "Áncash", "03": "Apurímac", "04": "Arequipa", "05": "Ayacucho",
	"06": "Cajamarca", "07": "Callao", "08": "Cusco", "09": "Huancavelica", "10": "Huánuco",
	"11": "Ica", "12": "Junín", "13": "La Libertad", "14": "Lambayeque", "15": "Lima",
	"16": "Loreto", "17": "Madre de Dios", "18": "Moquegua", "19": "Pasco", "20": "Piura",
	"21": "Puno", "22": "San Martín", "23": "Tacna", "24": "Tumbes", "25": "Ucayali",
}

const maxAddresses = 10

var ubigeoRe = regexp.MustCompile(`^\d{6}$`)

type Address struct {
	ID         id.ID
	AccountID  id.ID
	Label      string // "Consultorio Miraflores"
	Recipient  string
	Line1      string
	Reference  string
	District   string
	Province   string
	Department string // INEI code, see Departments
	Ubigeo     string // optional 6-digit INEI code; must start with Department
	Phone      string
	IsDefault  bool
	CreatedAt  time.Time
}

type AddressInput struct {
	Label, Recipient, Line1, Reference, District, Province, Department, Ubigeo, Phone string
}

func NewAddress(accountID id.ID, in AddressInput, now time.Time) (*Address, error) {
	a := &Address{ID: id.New(), AccountID: accountID, CreatedAt: now}
	if err := a.Update(in); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Address) Update(in AddressInput) error {
	clean := func(s string, max int, field string, required bool) (string, error) {
		s = strings.TrimSpace(s)
		if required && s == "" {
			return "", apperr.Invalid("identity.address_field_required", field, field+" is required")
		}
		if utf8.RuneCountInString(s) > max {
			return "", apperr.Invalid("identity.address_field_too_long", field, field+" is too long")
		}
		return s, nil
	}
	var err error
	out := Address{ID: a.ID, AccountID: a.AccountID, IsDefault: a.IsDefault, CreatedAt: a.CreatedAt}
	if out.Label, err = clean(in.Label, 60, "label", false); err != nil {
		return err
	}
	if out.Recipient, err = clean(in.Recipient, 120, "recipient", true); err != nil {
		return err
	}
	if out.Line1, err = clean(in.Line1, 200, "line1", true); err != nil {
		return err
	}
	if out.Reference, err = clean(in.Reference, 200, "reference", false); err != nil {
		return err
	}
	if out.District, err = clean(in.District, 80, "district", true); err != nil {
		return err
	}
	if out.Province, err = clean(in.Province, 80, "province", true); err != nil {
		return err
	}
	if _, ok := Departments[in.Department]; !ok {
		return apperr.Invalid("identity.department_invalid", "department", "unknown department")
	}
	out.Department = in.Department
	out.Ubigeo = strings.TrimSpace(in.Ubigeo)
	if out.Ubigeo != "" && (!ubigeoRe.MatchString(out.Ubigeo) || out.Ubigeo[:2] != out.Department) {
		return apperr.Invalid("identity.ubigeo_invalid", "ubigeo", "ubigeo must be 6 digits and match the department")
	}
	if out.Phone, err = clean(in.Phone, 20, "phone", false); err != nil {
		return err
	}
	if out.Phone != "" && !phoneRe.MatchString(out.Phone) {
		return apperr.Invalid("identity.phone_invalid", "phone", "invalid phone number")
	}
	*a = out
	return nil
}

// CanAddAddress enforces the per-account limit.
func CanAddAddress(existing int) error {
	if existing >= maxAddresses {
		return apperr.Invalid("identity.too_many_addresses", "addresses", "at most 10 addresses")
	}
	return nil
}
