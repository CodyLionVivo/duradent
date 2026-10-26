package domain

import (
	"testing"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

var now = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func code(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

func TestValidRUC(t *testing.T) {
	for _, ruc := range []string{"20100047218", "20131312955"} { // BCP, SUNAT
		if !ValidRUC(ruc) {
			t.Errorf("%s should be valid", ruc)
		}
	}
	for _, ruc := range []string{"20100047219", "2010004721", "30100047218", "2010004721X", ""} {
		if ValidRUC(ruc) {
			t.Errorf("%s should be invalid", ruc)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	if e, err := NormalizeEmail("  Dra.Perez@Clinica.PE "); err != nil || e != "dra.perez@clinica.pe" {
		t.Fatalf("got %q, %v", e, err)
	}
	for _, bad := range []string{"", "no-at", "a@b", "Ana <ana@x.pe>", "a@@x.pe"} {
		if _, err := NormalizeEmail(bad); code(err) != "identity.email_invalid" {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestCheckPassword(t *testing.T) {
	cases := map[string]string{
		"corta":                  "identity.password_short",
		"ortodoncia1":            "identity.password_weak",
		"dra.perez@clinica.pe":   "identity.password_weak",
		"brackets de acero 2026": "",
	}
	for pw, want := range cases {
		if got := code(CheckPassword(pw, "dra.perez@clinica.pe")); got != want {
			t.Errorf("%q: got %q, want %q", pw, got, want)
		}
	}
}

func TestClaimByVerifiedProviderDropsUntrustedPassword(t *testing.T) {
	a, _ := NewAccount("victima@clinica.pe", "", false, "", now)
	a.PasswordHash = "set-by-whoever-typed-the-email"
	if removed := a.ClaimByVerifiedProvider(now); !removed || a.PasswordHash != "" || !a.EmailVerified {
		t.Fatalf("unverified account must lose its password: %+v", a)
	}

	b, _ := NewAccount("dueno@clinica.pe", "", true, "", now)
	b.PasswordHash = "hash"
	if removed := b.ClaimByVerifiedProvider(now); removed || b.PasswordHash == "" {
		t.Fatal("a verified account keeps its password")
	}
}

func TestPromoteIfListedRequiresVerifiedEmail(t *testing.T) {
	admins := map[string]bool{"jefe@duradent.pe": true}
	a, _ := NewAccount("jefe@duradent.pe", "", false, "", now)
	if a.PromoteIfListed(admins, now) {
		t.Fatal("unverified email must not grant admin")
	}
	a.EmailVerified = true
	if !a.PromoteIfListed(admins, now) || a.Role != RoleAdmin {
		t.Fatal("verified listed email becomes admin")
	}
}

func TestCustomerValidation(t *testing.T) {
	acc := id.New()
	ok := []CustomerInput{
		{Type: CustomerPerson, DocumentType: DocDNI, DocumentNumber: "12345678"},
		{Type: CustomerPerson, DocumentType: DocRUC, DocumentNumber: "20100047218", COPNumber: "12345"},
		{Type: CustomerCompany, DocumentType: DocRUC, DocumentNumber: "20131312955", BusinessName: "Clínica Dental SAC", Phone: "+51 999 888 777"},
	}
	for _, in := range ok {
		if _, err := NewCustomer(acc, in, now); err != nil {
			t.Errorf("%+v: %v", in, err)
		}
	}
	bad := map[string]CustomerInput{
		"identity.dni_invalid":            {Type: CustomerPerson, DocumentType: DocDNI, DocumentNumber: "1234"},
		"identity.ruc_invalid":            {Type: CustomerPerson, DocumentType: DocRUC, DocumentNumber: "20100047219"},
		"identity.company_needs_ruc20":    {Type: CustomerCompany, DocumentType: DocDNI, DocumentNumber: "12345678", BusinessName: "X"},
		"identity.business_name_required": {Type: CustomerCompany, DocumentType: DocRUC, DocumentNumber: "20131312955"},
		"identity.cop_invalid":            {Type: CustomerPerson, DocumentType: DocDNI, DocumentNumber: "12345678", COPNumber: "ABC"},
		"identity.customer_type_invalid":  {Type: "otro", DocumentType: DocDNI, DocumentNumber: "12345678"},
	}
	for want, in := range bad {
		if _, err := NewCustomer(acc, in, now); code(err) != want {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestInvoiceType(t *testing.T) {
	ruc, _ := NewCustomer(id.New(), CustomerInput{Type: CustomerPerson, DocumentType: DocRUC, DocumentNumber: "20100047218"}, now)
	dni, _ := NewCustomer(id.New(), CustomerInput{Type: CustomerPerson, DocumentType: DocDNI, DocumentNumber: "12345678"}, now)
	if ruc.InvoiceType() != "01" || dni.InvoiceType() != "03" {
		t.Fatal("RUC → factura 01, DNI → boleta 03")
	}
}

func TestAddressValidation(t *testing.T) {
	in := AddressInput{Recipient: "Dra. Pérez", Line1: "Av. Larco 123", District: "Miraflores", Province: "Lima", Department: "15", Ubigeo: "150122"}
	if _, err := NewAddress(id.New(), in, now); err != nil {
		t.Fatal(err)
	}
	in.Ubigeo = "040101" // Arequipa ubigeo with Lima department
	if _, err := NewAddress(id.New(), in, now); code(err) != "identity.ubigeo_invalid" {
		t.Fatalf("got %v", err)
	}
	in.Ubigeo, in.Department = "", "99"
	if _, err := NewAddress(id.New(), in, now); code(err) != "identity.department_invalid" {
		t.Fatalf("got %v", err)
	}
	if code(CanAddAddress(10)) != "identity.too_many_addresses" {
		t.Fatal("limit of 10 addresses")
	}
}

func TestSessionTokenIsStoredHashed(t *testing.T) {
	s, token := NewSession(id.New(), "1.2.3.4", "test", now)
	if len(token) < 40 || s.TokenHash == token || s.TokenHash != HashToken(token) {
		t.Fatalf("token %q hash %q", token, s.TokenHash)
	}
	if s.Expired(now) || !s.Expired(now.Add(SessionTTL)) {
		t.Fatal("expiry")
	}
	_, other := NewSession(id.New(), "", "", now)
	if other == token {
		t.Fatal("tokens must be random")
	}
}
