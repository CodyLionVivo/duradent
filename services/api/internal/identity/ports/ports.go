// Package ports declares what the identity context needs from the outside.
package ports

import (
	"context"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/identity/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

type AccountRepository interface {
	// Create inserts the account and, if given, its first identity, atomically.
	// Returns apperr Conflict "identity.email_taken" for a duplicate email.
	Create(ctx context.Context, a *domain.Account, ident *domain.Identity) error
	Update(ctx context.Context, a *domain.Account) error
	ByID(ctx context.Context, accountID id.ID) (*domain.Account, error)
	ByEmail(ctx context.Context, email string) (*domain.Account, error)
	ByIdentity(ctx context.Context, p domain.Provider, subject string) (*domain.Account, error)
	LinkIdentity(ctx context.Context, accountID id.ID, ident domain.Identity) error
	List(ctx context.Context, query string, limit, offset int) ([]*domain.Account, int, error)
}

type CustomerRepository interface {
	Save(ctx context.Context, c *domain.Customer) error
	// ByAccount returns apperr NotFound when the account has no profile yet.
	ByAccount(ctx context.Context, accountID id.ID) (*domain.Customer, error)
	ByAccounts(ctx context.Context, accountIDs []id.ID) (map[id.ID]*domain.Customer, error)
}

type AddressRepository interface {
	List(ctx context.Context, accountID id.ID) ([]*domain.Address, error)
	Save(ctx context.Context, a *domain.Address) error
	Delete(ctx context.Context, accountID, addressID id.ID) error
	// SetDefault marks one address as default and clears the others.
	SetDefault(ctx context.Context, accountID, addressID id.ID) error
}

type SessionRepository interface {
	Create(ctx context.Context, s domain.Session) error
	ByTokenHash(ctx context.Context, hash string) (domain.Session, error)
	Delete(ctx context.Context, hash string) error
	DeleteForAccount(ctx context.Context, accountID id.ID) error
}

// PasswordHasher hashes with a slow, salted KDF (Argon2id).
type PasswordHasher interface {
	Hash(password string) (string, error)
	Verify(password, hash string) (bool, error)
}

// LoginState is the server-side half of an OIDC authorization request; it is
// consumed exactly once when the provider redirects back.
type LoginState struct {
	State       string
	Provider    domain.Provider
	Nonce       string
	Verifier    string // PKCE code verifier
	RedirectURI string
	ReturnTo    string
	ExpiresAt   time.Time
}

type LoginStateRepository interface {
	Save(ctx context.Context, s LoginState) error
	// Take returns and deletes the state; NotFound if missing or already used.
	Take(ctx context.Context, state string) (LoginState, error)
}

// Claims are the verified facts an identity provider asserts about a user.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// OIDCProvider runs the authorization code flow with PKCE against one provider.
type OIDCProvider interface {
	AuthCodeURL(state, nonce, verifier, redirectURI string) string
	// Exchange trades the code for tokens and returns the verified ID token
	// claims; it checks signature, issuer, audience, expiry and nonce.
	Exchange(ctx context.Context, code, verifier, redirectURI, nonce string) (Claims, error)
}

// Verification is a pending email confirmation; only the token's hash is stored.
type Verification struct {
	TokenHash string
	AccountID id.ID
	Email     string
	ExpiresAt time.Time
	CreatedAt time.Time
}

type VerificationRepository interface {
	Create(ctx context.Context, v Verification) error
	// Take returns and deletes the verification; NotFound if unknown.
	Take(ctx context.Context, tokenHash string) (Verification, error)
	// Recent counts verifications created for the account since t (throttling).
	Recent(ctx context.Context, accountID id.ID, since time.Time) (int, error)
}

// PasswordReset is a pending "forgot your password" link; only the token's
// hash is stored.
type PasswordReset struct {
	TokenHash string
	AccountID id.ID
	Email     string // the address the link was sent to
	ExpiresAt time.Time
	CreatedAt time.Time
}

type PasswordResetRepository interface {
	Create(ctx context.Context, r PasswordReset) error
	// Get returns a reset without using it up; NotFound if unknown.
	Get(ctx context.Context, tokenHash string) (PasswordReset, error)
	// Take returns and deletes the reset and every other pending link of its
	// account; NotFound if it was already used.
	Take(ctx context.Context, tokenHash string) (PasswordReset, error)
	// Recent counts resets created for the account since t (throttling).
	Recent(ctx context.Context, accountID id.ID, since time.Time) (int, error)
}

// AccountMailer emails the links identity hands out: email confirmation and
// password reset. Tokens never travel through the event bus.
type AccountMailer interface {
	SendVerification(ctx context.Context, email, name string, l locale.Locale, token string) error
	SendPasswordReset(ctx context.Context, email, name string, l locale.Locale, token string) error
}

type UnitOfWork interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Events records domain events in the current transaction (outbox).
type Events interface {
	Record(ctx context.Context, aggregateType string, aggregateID id.ID, eventType string, payload any, at time.Time) error
}
