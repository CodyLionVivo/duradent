// Package ports declares what the identity context needs from the outside.
package ports

import (
	"context"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/identity/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
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
