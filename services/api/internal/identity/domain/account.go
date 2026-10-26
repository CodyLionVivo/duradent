// Package domain models who buys: accounts (how someone signs in), their
// customer profile (what goes on invoices) and sessions.
package domain

import (
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

type Role string

const (
	RoleCustomer Role = "customer"
	RoleAdmin    Role = "admin"
)

type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
)

// Provider identifies an external identity provider.
type Provider string

const (
	ProviderGoogle    Provider = "google"
	ProviderMicrosoft Provider = "microsoft"
	ProviderDev       Provider = "dev" // local development only
)

// Account is a person who can sign in. One account may have a password,
// several linked OIDC identities, or both.
type Account struct {
	ID            id.ID
	Email         string // normalized: trimmed, lowercase
	EmailVerified bool
	PasswordHash  string // empty when the account only signs in with OIDC
	DisplayName   string
	Locale        locale.Locale
	Role          Role
	Status        Status
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Identity links an account to a subject at an external provider.
type Identity struct {
	Provider Provider
	Subject  string
	Email    string
}

// NormalizeEmail validates and canonicalizes an email address.
func NormalizeEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || len(e) > 254 || !strings.Contains(e[strings.LastIndex(e, "@")+1:], ".") {
		return "", apperr.Invalid("identity.email_invalid", "email", "invalid email address")
	}
	return e, nil
}

func cleanName(name, email string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.Split(email, "@")[0]
	}
	if utf8.RuneCountInString(name) > 120 {
		return "", apperr.Invalid("identity.name_too_long", "display_name", "name is too long")
	}
	return name, nil
}

// NewAccount creates an active customer account.
func NewAccount(email, displayName string, verified bool, l locale.Locale, now time.Time) (*Account, error) {
	e, err := NormalizeEmail(email)
	if err != nil {
		return nil, err
	}
	name, err := cleanName(displayName, e)
	if err != nil {
		return nil, err
	}
	if l == "" {
		l = locale.Default
	}
	return &Account{
		ID: id.New(), Email: e, EmailVerified: verified, DisplayName: name, Locale: l,
		Role: RoleCustomer, Status: StatusActive, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (a *Account) Active() bool { return a.Status == StatusActive }

// ClaimByVerifiedProvider is called when someone proves ownership of this
// account's email through a provider. If the email was never verified, a
// password may have been set by someone else who typed this address, so the
// password is dropped; the caller must also revoke existing sessions.
// It reports whether the password was removed.
func (a *Account) ClaimByVerifiedProvider(now time.Time) (passwordRemoved bool) {
	if a.EmailVerified {
		return false
	}
	passwordRemoved = a.PasswordHash != ""
	a.PasswordHash = ""
	a.EmailVerified = true
	a.UpdatedAt = now
	return passwordRemoved
}

// PromoteIfListed makes the account an admin when its verified email is in
// adminEmails. Unverified emails never grant admin.
func (a *Account) PromoteIfListed(adminEmails map[string]bool, now time.Time) bool {
	if a.Role == RoleAdmin || !a.EmailVerified || !adminEmails[a.Email] {
		return false
	}
	a.Role = RoleAdmin
	a.UpdatedAt = now
	return true
}

func (a *Account) SetRole(r Role, now time.Time) error {
	if r != RoleCustomer && r != RoleAdmin {
		return apperr.Invalid("identity.role_invalid", "role", "unknown role")
	}
	a.Role = r
	a.UpdatedAt = now
	return nil
}

func (a *Account) Rename(name string, l locale.Locale, now time.Time) error {
	n, err := cleanName(name, a.Email)
	if err != nil {
		return err
	}
	a.DisplayName = n
	if l != "" {
		a.Locale = l
	}
	a.UpdatedAt = now
	return nil
}

// --- passwords ---------------------------------------------------------------------

const (
	minPasswordLen = 10
	maxPasswordLen = 128
)

// commonPasswords is a short deny-list of the passwords attackers try first.
var commonPasswords = map[string]bool{
	"1234567890": true, "12345678910": true, "contraseña": true, "contrasena1": true,
	"password12": true, "password123": true, "qwertyuiop": true, "duradent123": true,
	"ortodoncia": true, "ortodoncia1": true, "0987654321": true, "1111111111": true,
}

// CheckPassword enforces the password policy: length 10–128 (any characters,
// passphrases welcome), not a well-known password, not the email itself.
func CheckPassword(password, email string) error {
	n := utf8.RuneCountInString(password)
	if n < minPasswordLen {
		return apperr.Invalid("identity.password_short", "password", "use at least 10 characters")
	}
	if n > maxPasswordLen {
		return apperr.Invalid("identity.password_long", "password", "use at most 128 characters")
	}
	lower := strings.ToLower(password)
	if commonPasswords[lower] || lower == strings.ToLower(email) || lower == strings.Split(strings.ToLower(email), "@")[0] {
		return apperr.Invalid("identity.password_weak", "password", "this password is too easy to guess")
	}
	return nil
}

// ConfirmEmail marks the email as proven. The link only counts for the email
// it was sent to.
func (a *Account) ConfirmEmail(email string, now time.Time) error {
	if email != a.Email {
		return apperr.Conflict("identity.verification_stale", "token", "this link was sent to a different email")
	}
	a.EmailVerified = true
	a.UpdatedAt = now
	return nil
}
