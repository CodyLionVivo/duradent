// Package application implements sign-up, sign-in (password and OIDC),
// sessions, customer profiles and account administration.
package application

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/identity/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/identity/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
)

const loginStateTTL = 10 * time.Minute

// Config holds the identity policy decided at deploy time.
type Config struct {
	// AdminEmails become admins once they sign in with a verified email.
	AdminEmails []string
	// DevLogin enables password-less sign-in by email. Local development only.
	DevLogin bool
}

type Service struct {
	accounts  ports.AccountRepository
	customers ports.CustomerRepository
	addresses ports.AddressRepository
	sessions  ports.SessionRepository
	states    ports.LoginStateRepository
	hasher    ports.PasswordHasher
	providers map[domain.Provider]ports.OIDCProvider
	admins    map[string]bool
	devLogin  bool
	now       func() time.Time
	verifs    ports.VerificationRepository
	mailer    ports.VerificationMailer
	uow       ports.UnitOfWork
	events    ports.Events

	// dummyHash is verified against when an email is unknown, so a failed
	// login takes the same time whether or not the account exists.
	dummyHash string
}

type Deps struct {
	Accounts  ports.AccountRepository
	Customers ports.CustomerRepository
	Addresses ports.AddressRepository
	Sessions  ports.SessionRepository
	States    ports.LoginStateRepository
	Hasher    ports.PasswordHasher
	Providers map[domain.Provider]ports.OIDCProvider
	Now       func() time.Time

	Verifications ports.VerificationRepository
	Mailer        ports.VerificationMailer
	UoW           ports.UnitOfWork
	Events        ports.Events
}

func NewService(d Deps, cfg Config) (*Service, error) {
	admins := map[string]bool{}
	for _, e := range cfg.AdminEmails {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			admins[e] = true
		}
	}
	dummy, err := d.Hasher.Hash("dummy password for timing equalization")
	if err != nil {
		return nil, err
	}
	if d.Providers == nil {
		d.Providers = map[domain.Provider]ports.OIDCProvider{}
	}
	return &Service{
		accounts: d.Accounts, customers: d.Customers, addresses: d.Addresses, sessions: d.Sessions,
		states: d.States, hasher: d.Hasher, providers: d.Providers, admins: admins,
		devLogin: cfg.DevLogin, now: d.Now, dummyHash: dummy,
		verifs: d.Verifications, mailer: d.Mailer, uow: d.UoW, events: d.Events,
	}, nil
}

// Providers lists the enabled sign-in providers (for the login page).
func (s *Service) Providers() []domain.Provider {
	var out []domain.Provider
	for _, p := range []domain.Provider{domain.ProviderGoogle, domain.ProviderMicrosoft} {
		if _, ok := s.providers[p]; ok {
			out = append(out, p)
		}
	}
	if s.devLogin {
		out = append(out, domain.ProviderDev)
	}
	return out
}

// Client describes where a sign-in request comes from, for the session record.
type Client struct {
	IP        string
	UserAgent string
}

// SignedIn is the result of any successful sign-in.
type SignedIn struct {
	Account *domain.Account
	Token   string
	Expires time.Time
}

func (s *Service) startSession(ctx context.Context, a *domain.Account, c Client) (SignedIn, error) {
	sess, token := domain.NewSession(a.ID, c.IP, c.UserAgent, s.now())
	if err := s.sessions.Create(ctx, sess); err != nil {
		return SignedIn{}, err
	}
	return SignedIn{Account: a, Token: token, Expires: sess.ExpiresAt}, nil
}

var errCredentials = apperr.Invalid("identity.credentials_invalid", "", "email or password is incorrect")

// --- Password accounts -----------------------------------------------------------------

type RegisterInput struct {
	Email, Password, Name string
	Locale                locale.Locale
}

// Register creates a password account. Its email stays unverified until the
// owner opens the emailed link, so it never grants admin and a verified
// provider sign-in can reclaim it.
func (s *Service) Register(ctx context.Context, in RegisterInput, c Client) (SignedIn, error) {
	a, err := domain.NewAccount(in.Email, in.Name, false, in.Locale, s.now())
	if err != nil {
		return SignedIn{}, err
	}
	if err := domain.CheckPassword(in.Password, a.Email); err != nil {
		return SignedIn{}, err
	}
	if a.PasswordHash, err = s.hasher.Hash(in.Password); err != nil {
		return SignedIn{}, err
	}
	if err := s.create(ctx, a, nil); err != nil {
		return SignedIn{}, err
	}
	s.sendVerification(ctx, a)
	return s.startSession(ctx, a, c)
}

func (s *Service) Login(ctx context.Context, email, password string, c Client) (SignedIn, error) {
	e, err := domain.NormalizeEmail(email)
	if err != nil {
		_, _ = s.hasher.Verify(password, s.dummyHash)
		return SignedIn{}, errCredentials
	}
	a, err := s.accounts.ByEmail(ctx, e)
	if err != nil && !apperr.IsKind(err, apperr.KindNotFound) {
		return SignedIn{}, err
	}
	if a == nil || a.PasswordHash == "" {
		_, _ = s.hasher.Verify(password, s.dummyHash)
		return SignedIn{}, errCredentials
	}
	ok, err := s.hasher.Verify(password, a.PasswordHash)
	if err != nil {
		return SignedIn{}, err
	}
	if !ok {
		return SignedIn{}, errCredentials
	}
	if !a.Active() {
		return SignedIn{}, apperr.Invalid("identity.account_disabled", "", "this account is disabled")
	}
	return s.startSession(ctx, a, c)
}

// ChangePassword sets or replaces the password of a signed-in account.
func (s *Service) ChangePassword(ctx context.Context, accountID id.ID, current, next string) error {
	a, err := s.accounts.ByID(ctx, accountID)
	if err != nil {
		return err
	}
	if a.PasswordHash != "" {
		ok, err := s.hasher.Verify(current, a.PasswordHash)
		if err != nil {
			return err
		}
		if !ok {
			return apperr.Invalid("identity.password_current_wrong", "current_password", "current password is incorrect")
		}
	}
	if err := domain.CheckPassword(next, a.Email); err != nil {
		return err
	}
	if a.PasswordHash, err = s.hasher.Hash(next); err != nil {
		return err
	}
	a.UpdatedAt = s.now()
	return s.accounts.Update(ctx, a)
}

// --- OIDC --------------------------------------------------------------------------------

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// StartOIDC prepares an authorization request and returns the provider URL
// and the state, which the web also binds to the browser with a cookie.
func (s *Service) StartOIDC(ctx context.Context, p domain.Provider, redirectURI, returnTo string) (url, state string, err error) {
	provider, ok := s.providers[p]
	if !ok {
		return "", "", apperr.NotFound("identity.provider_unknown", "sign-in provider not enabled")
	}
	ls := ports.LoginState{
		State: randomToken(24), Provider: p, Nonce: randomToken(24), Verifier: randomToken(48),
		RedirectURI: redirectURI, ReturnTo: returnTo, ExpiresAt: s.now().Add(loginStateTTL),
	}
	if err := s.states.Save(ctx, ls); err != nil {
		return "", "", err
	}
	return provider.AuthCodeURL(ls.State, ls.Nonce, ls.Verifier, redirectURI), ls.State, nil
}

// FinishOIDC completes the flow when the provider redirects back.
func (s *Service) FinishOIDC(ctx context.Context, p domain.Provider, code, state string, c Client) (SignedIn, string, error) {
	invalid := apperr.Invalid("identity.login_state_invalid", "", "sign-in expired or was already used; please try again")
	ls, err := s.states.Take(ctx, state)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return SignedIn{}, "", invalid
		}
		return SignedIn{}, "", err
	}
	if ls.Provider != p || !s.now().Before(ls.ExpiresAt) {
		return SignedIn{}, "", invalid
	}
	provider, ok := s.providers[p]
	if !ok {
		return SignedIn{}, "", apperr.NotFound("identity.provider_unknown", "sign-in provider not enabled")
	}
	claims, err := provider.Exchange(ctx, code, ls.Verifier, ls.RedirectURI, ls.Nonce)
	if err != nil {
		return SignedIn{}, "", apperr.Invalid("identity.provider_rejected", "", "the provider did not confirm the sign-in")
	}
	signed, err := s.signInWithClaims(ctx, p, claims, c)
	return signed, ls.ReturnTo, err
}

// DevLogin signs in by email without a password. Only for local development.
func (s *Service) DevLogin(ctx context.Context, email, name string, c Client) (SignedIn, error) {
	if !s.devLogin {
		return SignedIn{}, apperr.NotFound("identity.provider_unknown", "sign-in provider not enabled")
	}
	e, err := domain.NormalizeEmail(email)
	if err != nil {
		return SignedIn{}, err
	}
	return s.signInWithClaims(ctx, domain.ProviderDev, ports.Claims{Subject: e, Email: e, EmailVerified: true, Name: name}, c)
}

// signInWithClaims finds or creates the account for a provider identity:
//  1. a known (provider, subject) signs into its account;
//  2. a verified email matching an existing account links to it (and drops a
//     password nobody had proven ownership of);
//  3. otherwise a new account is created.
func (s *Service) signInWithClaims(ctx context.Context, p domain.Provider, cl ports.Claims, c Client) (SignedIn, error) {
	if cl.Subject == "" {
		return SignedIn{}, apperr.Invalid("identity.provider_rejected", "", "the provider did not return a user id")
	}
	now := s.now()
	ident := domain.Identity{Provider: p, Subject: cl.Subject, Email: strings.ToLower(cl.Email)}

	a, err := s.accounts.ByIdentity(ctx, p, cl.Subject)
	if err != nil && !apperr.IsKind(err, apperr.KindNotFound) {
		return SignedIn{}, err
	}
	if a == nil {
		email, err := domain.NormalizeEmail(cl.Email)
		if err != nil {
			return SignedIn{}, apperr.Invalid("identity.provider_no_email", "", "the provider did not share a valid email")
		}
		existing, err := s.accounts.ByEmail(ctx, email)
		if err != nil && !apperr.IsKind(err, apperr.KindNotFound) {
			return SignedIn{}, err
		}
		switch {
		case existing != nil && cl.EmailVerified:
			if existing.ClaimByVerifiedProvider(now) {
				if err := s.sessions.DeleteForAccount(ctx, existing.ID); err != nil {
					return SignedIn{}, err
				}
			}
			if err := s.accounts.LinkIdentity(ctx, existing.ID, ident); err != nil {
				return SignedIn{}, err
			}
			a = existing
		case existing != nil:
			return SignedIn{}, apperr.Conflict("identity.email_in_use", "email",
				"an account with this email exists; sign in with your password first")
		default:
			if a, err = domain.NewAccount(email, cl.Name, cl.EmailVerified, "", now); err != nil {
				return SignedIn{}, err
			}
			if err := s.create(ctx, a, &ident); err != nil {
				return SignedIn{}, err
			}
			if !a.EmailVerified {
				s.sendVerification(ctx, a)
			}
		}
	}
	if !a.Active() {
		return SignedIn{}, apperr.Invalid("identity.account_disabled", "", "this account is disabled")
	}
	a.PromoteIfListed(s.admins, now)
	if err := s.accounts.Update(ctx, a); err != nil {
		return SignedIn{}, err
	}
	return s.startSession(ctx, a, c)
}

// --- Sessions ------------------------------------------------------------------------------

// Principal is the authenticated caller of a request.
type Principal struct {
	Account       *domain.Account
	PriceListCode string // empty: public list
}

// Authenticate resolves a session token. Unknown, expired or disabled
// sessions are reported as NotFound so callers treat them as anonymous.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	unauth := apperr.NotFound("identity.session_invalid", "not signed in")
	if token == "" {
		return Principal{}, unauth
	}
	hash := domain.HashToken(token)
	sess, err := s.sessions.ByTokenHash(ctx, hash)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return Principal{}, unauth
		}
		return Principal{}, err
	}
	if sess.Expired(s.now()) {
		_ = s.sessions.Delete(ctx, hash)
		return Principal{}, unauth
	}
	a, err := s.accounts.ByID(ctx, sess.AccountID)
	if err != nil || !a.Active() {
		return Principal{}, unauth
	}
	p := Principal{Account: a}
	if c, err := s.customers.ByAccount(ctx, a.ID); err == nil {
		p.PriceListCode = c.PriceListCode
	} else if !apperr.IsKind(err, apperr.KindNotFound) {
		return Principal{}, err
	}
	return p, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.sessions.Delete(ctx, domain.HashToken(token))
}

// --- Profile -------------------------------------------------------------------------------

type Profile struct {
	Account   *domain.Account
	Customer  *domain.Customer // nil until the billing profile is filled in
	Addresses []*domain.Address
}

func (s *Service) Profile(ctx context.Context, accountID id.ID) (Profile, error) {
	a, err := s.accounts.ByID(ctx, accountID)
	if err != nil {
		return Profile{}, err
	}
	p := Profile{Account: a}
	if c, err := s.customers.ByAccount(ctx, accountID); err == nil {
		p.Customer = c
	} else if !apperr.IsKind(err, apperr.KindNotFound) {
		return Profile{}, err
	}
	if p.Addresses, err = s.addresses.List(ctx, accountID); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func (s *Service) UpdateAccount(ctx context.Context, accountID id.ID, name string, l locale.Locale) error {
	a, err := s.accounts.ByID(ctx, accountID)
	if err != nil {
		return err
	}
	if err := a.Rename(name, l, s.now()); err != nil {
		return err
	}
	return s.accounts.Update(ctx, a)
}

// SaveCustomer creates or updates the billing profile. The price list is
// kept: only admins change it.
func (s *Service) SaveCustomer(ctx context.Context, accountID id.ID, in domain.CustomerInput) (*domain.Customer, error) {
	c, err := s.customers.ByAccount(ctx, accountID)
	switch {
	case err == nil:
		if err := c.Update(in, s.now()); err != nil {
			return nil, err
		}
	case apperr.IsKind(err, apperr.KindNotFound):
		if c, err = domain.NewCustomer(accountID, in, s.now()); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	if err := s.customers.Save(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) AddAddress(ctx context.Context, accountID id.ID, in domain.AddressInput) (*domain.Address, error) {
	existing, err := s.addresses.List(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if err := domain.CanAddAddress(len(existing)); err != nil {
		return nil, err
	}
	a, err := domain.NewAddress(accountID, in, s.now())
	if err != nil {
		return nil, err
	}
	a.IsDefault = len(existing) == 0
	if err := s.addresses.Save(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Service) UpdateAddress(ctx context.Context, accountID, addressID id.ID, in domain.AddressInput) (*domain.Address, error) {
	a, err := s.ownAddress(ctx, accountID, addressID)
	if err != nil {
		return nil, err
	}
	if err := a.Update(in); err != nil {
		return nil, err
	}
	return a, s.addresses.Save(ctx, a)
}

func (s *Service) RemoveAddress(ctx context.Context, accountID, addressID id.ID) error {
	a, err := s.ownAddress(ctx, accountID, addressID)
	if err != nil {
		return err
	}
	if err := s.addresses.Delete(ctx, accountID, addressID); err != nil {
		return err
	}
	if a.IsDefault {
		rest, err := s.addresses.List(ctx, accountID)
		if err == nil && len(rest) > 0 {
			return s.addresses.SetDefault(ctx, accountID, rest[0].ID)
		}
		return err
	}
	return nil
}

func (s *Service) SetDefaultAddress(ctx context.Context, accountID, addressID id.ID) error {
	if _, err := s.ownAddress(ctx, accountID, addressID); err != nil {
		return err
	}
	return s.addresses.SetDefault(ctx, accountID, addressID)
}

// ownAddress loads an address only if it belongs to the account, so one
// customer can never read or change another's addresses.
func (s *Service) ownAddress(ctx context.Context, accountID, addressID id.ID) (*domain.Address, error) {
	list, err := s.addresses.List(ctx, accountID)
	if err != nil {
		return nil, err
	}
	for _, a := range list {
		if a.ID == addressID {
			return a, nil
		}
	}
	return nil, apperr.NotFound("identity.address_not_found", "address not found")
}

// --- Admin ---------------------------------------------------------------------------------

type AccountRow struct {
	Account  *domain.Account
	Customer *domain.Customer
}

func (s *Service) Accounts(ctx context.Context, query string, limit, offset int) ([]AccountRow, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	accounts, total, err := s.accounts.List(ctx, strings.TrimSpace(query), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	ids := make([]id.ID, len(accounts))
	for i, a := range accounts {
		ids[i] = a.ID
	}
	customers, err := s.customers.ByAccounts(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	rows := make([]AccountRow, len(accounts))
	for i, a := range accounts {
		rows[i] = AccountRow{Account: a, Customer: customers[a.ID]}
	}
	return rows, total, nil
}

// SetRole changes a user's role. Admins cannot demote themselves, so the
// panel is never left without the person who was using it.
func (s *Service) SetRole(ctx context.Context, actor, target id.ID, r domain.Role) error {
	if actor == target && r != domain.RoleAdmin {
		return apperr.Conflict("identity.self_demotion", "role", "you cannot remove your own admin role")
	}
	a, err := s.accounts.ByID(ctx, target)
	if err != nil {
		return err
	}
	if err := a.SetRole(r, s.now()); err != nil {
		return err
	}
	return s.accounts.Update(ctx, a)
}

// AssignPriceList sets which price list a customer buys from. The customer
// must have filled in a billing profile first. listExists is checked by the
// caller against the pricing context.
func (s *Service) AssignPriceList(ctx context.Context, accountID id.ID, code string) error {
	c, err := s.customers.ByAccount(ctx, accountID)
	if err != nil {
		if apperr.IsKind(err, apperr.KindNotFound) {
			return apperr.Conflict("identity.customer_profile_missing", "price_list", "the customer has no billing profile yet")
		}
		return err
	}
	c.AssignPriceList(code, s.now())
	return s.customers.Save(ctx, c)
}

// --- Registration events and email verification -----------------------------------------

const (
	verificationTTL = 24 * time.Hour
	// maxVerificationsPerHour throttles "resend" so the endpoint cannot be
	// used to flood a mailbox.
	maxVerificationsPerHour = 3
)

// create stores a new account and its CustomerRegistered event atomically.
func (s *Service) create(ctx context.Context, a *domain.Account, ident *domain.Identity) error {
	return s.uow.InTx(ctx, func(ctx context.Context) error {
		if err := s.accounts.Create(ctx, a, ident); err != nil {
			return err
		}
		return s.events.Record(ctx, "account", a.ID, "CustomerRegistered", map[string]any{
			"account_id": a.ID, "email": a.Email, "locale": a.Locale, "email_verified": a.EmailVerified,
		}, s.now())
	})
}

// sendVerification emails a confirmation link. A failure is logged by the
// mailer and does not fail the sign-up: the customer can ask for a new link.
func (s *Service) sendVerification(ctx context.Context, a *domain.Account) {
	_ = s.issueVerification(ctx, a)
}

func (s *Service) issueVerification(ctx context.Context, a *domain.Account) error {
	now := s.now()
	n, err := s.verifs.Recent(ctx, a.ID, now.Add(-time.Hour))
	if err != nil {
		return err
	}
	if n >= maxVerificationsPerHour {
		return apperr.Conflict("identity.verification_throttled", "", "we already sent several links; check your inbox or try again later")
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	if err := s.verifs.Create(ctx, ports.Verification{
		TokenHash: domain.HashToken(token), AccountID: a.ID, Email: a.Email, ExpiresAt: now.Add(verificationTTL), CreatedAt: now,
	}); err != nil {
		return err
	}
	return s.mailer.SendVerification(ctx, a.Email, a.DisplayName, a.Locale, token)
}

// ResendVerification sends a new link to a signed-in customer.
func (s *Service) ResendVerification(ctx context.Context, accountID id.ID) error {
	a, err := s.accounts.ByID(ctx, accountID)
	if err != nil {
		return err
	}
	if a.EmailVerified {
		return apperr.Conflict("identity.already_verified", "", "your email is already confirmed")
	}
	return s.issueVerification(ctx, a)
}

// VerifyEmail consumes a link token and confirms the account's email.
func (s *Service) VerifyEmail(ctx context.Context, token string) error {
	invalid := apperr.Invalid("identity.verification_invalid", "token", "this link is invalid or has expired")
	if token == "" || len(token) > 100 {
		return invalid
	}
	v, err := s.verifs.Take(ctx, domain.HashToken(token))
	if apperr.IsKind(err, apperr.KindNotFound) {
		return invalid
	}
	if err != nil {
		return err
	}
	now := s.now()
	if !now.Before(v.ExpiresAt) {
		return invalid
	}
	a, err := s.accounts.ByID(ctx, v.AccountID)
	if err != nil {
		return err
	}
	if err := a.ConfirmEmail(v.Email, now); err != nil {
		return err
	}
	a.PromoteIfListed(s.admins, now)
	return s.accounts.Update(ctx, a)
}
