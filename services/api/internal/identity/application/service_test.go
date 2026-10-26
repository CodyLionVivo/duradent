package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/identity/application"
	"github.com/CodyLionVivo/duradent/services/api/internal/identity/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/identity/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

// --- fakes ---------------------------------------------------------------------------

type memAccounts struct {
	byID  map[id.ID]*domain.Account
	links map[string]id.ID // provider|subject → account
}

func (m *memAccounts) Create(_ context.Context, a *domain.Account, ident *domain.Identity) error {
	for _, o := range m.byID {
		if o.Email == a.Email {
			return apperr.Conflict("identity.email_taken", "email", "taken")
		}
	}
	cp := *a
	m.byID[a.ID] = &cp
	if ident != nil {
		m.links[string(ident.Provider)+"|"+ident.Subject] = a.ID
	}
	return nil
}
func (m *memAccounts) Update(_ context.Context, a *domain.Account) error {
	cp := *a
	m.byID[a.ID] = &cp
	return nil
}
func (m *memAccounts) ByID(_ context.Context, aid id.ID) (*domain.Account, error) {
	if a, ok := m.byID[aid]; ok {
		cp := *a
		return &cp, nil
	}
	return nil, apperr.NotFound("identity.account_not_found", "nope")
}
func (m *memAccounts) ByEmail(ctx context.Context, email string) (*domain.Account, error) {
	for _, a := range m.byID {
		if a.Email == email {
			return m.ByID(ctx, a.ID)
		}
	}
	return nil, apperr.NotFound("identity.account_not_found", "nope")
}
func (m *memAccounts) ByIdentity(ctx context.Context, p domain.Provider, sub string) (*domain.Account, error) {
	if aid, ok := m.links[string(p)+"|"+sub]; ok {
		return m.ByID(ctx, aid)
	}
	return nil, apperr.NotFound("identity.account_not_found", "nope")
}
func (m *memAccounts) LinkIdentity(_ context.Context, aid id.ID, ident domain.Identity) error {
	m.links[string(ident.Provider)+"|"+ident.Subject] = aid
	return nil
}
func (m *memAccounts) List(context.Context, string, int, int) ([]*domain.Account, int, error) {
	var out []*domain.Account
	for _, a := range m.byID {
		out = append(out, a)
	}
	return out, len(out), nil
}

type memCustomers map[id.ID]*domain.Customer

func (m memCustomers) Save(_ context.Context, c *domain.Customer) error {
	m[c.AccountID] = c
	return nil
}
func (m memCustomers) ByAccount(_ context.Context, aid id.ID) (*domain.Customer, error) {
	if c, ok := m[aid]; ok {
		return c, nil
	}
	return nil, apperr.NotFound("identity.customer_not_found", "nope")
}
func (m memCustomers) ByAccounts(_ context.Context, ids []id.ID) (map[id.ID]*domain.Customer, error) {
	out := map[id.ID]*domain.Customer{}
	for _, i := range ids {
		if c, ok := m[i]; ok {
			out[i] = c
		}
	}
	return out, nil
}

type memAddresses map[id.ID]*domain.Address

func (m memAddresses) List(_ context.Context, aid id.ID) ([]*domain.Address, error) {
	var out []*domain.Address
	for _, a := range m {
		if a.AccountID == aid {
			cp := *a
			out = append(out, &cp)
		}
	}
	return out, nil
}
func (m memAddresses) Save(_ context.Context, a *domain.Address) error {
	cp := *a
	m[a.ID] = &cp
	return nil
}
func (m memAddresses) Delete(_ context.Context, _, addrID id.ID) error { delete(m, addrID); return nil }
func (m memAddresses) SetDefault(_ context.Context, aid, addrID id.ID) error {
	for _, a := range m {
		if a.AccountID == aid {
			a.IsDefault = a.ID == addrID
		}
	}
	return nil
}

type memSessions map[string]domain.Session

func (m memSessions) Create(_ context.Context, s domain.Session) error {
	m[s.TokenHash] = s
	return nil
}
func (m memSessions) ByTokenHash(_ context.Context, h string) (domain.Session, error) {
	if s, ok := m[h]; ok {
		return s, nil
	}
	return domain.Session{}, apperr.NotFound("identity.session_not_found", "nope")
}
func (m memSessions) Delete(_ context.Context, h string) error { delete(m, h); return nil }
func (m memSessions) DeleteForAccount(_ context.Context, aid id.ID) error {
	for h, s := range m {
		if s.AccountID == aid {
			delete(m, h)
		}
	}
	return nil
}

type memStates map[string]ports.LoginState

func (m memStates) Save(_ context.Context, s ports.LoginState) error { m[s.State] = s; return nil }
func (m memStates) Take(_ context.Context, state string) (ports.LoginState, error) {
	s, ok := m[state]
	if !ok {
		return ports.LoginState{}, apperr.NotFound("identity.state_not_found", "nope")
	}
	delete(m, state)
	return s, nil
}

// plainHasher keeps tests fast; production uses Argon2id.
type plainHasher struct{}

func (plainHasher) Hash(pw string) (string, error)       { return "h:" + pw, nil }
func (plainHasher) Verify(pw, hash string) (bool, error) { return hash == "h:"+pw, nil }

type fakeProvider struct {
	claims   ports.Claims
	lastURL  string
	exchange func(code, verifier, nonce string) error
}

func (f *fakeProvider) AuthCodeURL(state, nonce, verifier, redirect string) string {
	f.lastURL = "https://idp.test/auth?state=" + state + "&nonce=" + nonce + "&redirect=" + redirect
	return f.lastURL
}
func (f *fakeProvider) Exchange(_ context.Context, code, verifier, _, nonce string) (ports.Claims, error) {
	if f.exchange != nil {
		if err := f.exchange(code, verifier, nonce); err != nil {
			return ports.Claims{}, err
		}
	}
	return f.claims, nil
}

type fixture struct {
	svc      *application.Service
	accounts *memAccounts
	sessions memSessions
	google   *fakeProvider
	clock    *time.Time
}

func newFixture(t *testing.T, cfg application.Config) fixture {
	t.Helper()
	clock := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	f := fixture{
		accounts: &memAccounts{byID: map[id.ID]*domain.Account{}, links: map[string]id.ID{}},
		sessions: memSessions{},
		google:   &fakeProvider{},
		clock:    &clock,
	}
	svc, err := application.NewService(application.Deps{
		Accounts: f.accounts, Customers: memCustomers{}, Addresses: memAddresses{}, Sessions: f.sessions,
		States: memStates{}, Hasher: plainHasher{},
		Providers: map[domain.Provider]ports.OIDCProvider{domain.ProviderGoogle: f.google},
		Now:       func() time.Time { return *f.clock },
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.svc = svc
	return f
}

var client = application.Client{IP: "190.1.2.3", UserAgent: "test"}

func codeOf(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

// googleLogin runs the whole OIDC round trip with the given claims.
func (f fixture) googleLogin(t *testing.T, claims ports.Claims) (application.SignedIn, error) {
	t.Helper()
	f.google.claims = claims
	url, state, err := f.svc.StartOIDC(context.Background(), domain.ProviderGoogle, "http://web/callback", "/es/cuenta")
	if err != nil || !strings.Contains(url, state) {
		t.Fatalf("start: %q %v", url, err)
	}
	signed, ret, err := f.svc.FinishOIDC(context.Background(), domain.ProviderGoogle, "code", state, client)
	if err == nil && ret != "/es/cuenta" {
		t.Fatalf("return_to lost: %q", ret)
	}
	return signed, err
}

// --- tests ----------------------------------------------------------------------------

func TestRegisterLoginLogout(t *testing.T) {
	f := newFixture(t, application.Config{})
	ctx := context.Background()

	reg, err := f.svc.Register(ctx, application.RegisterInput{Email: "Dra.Ana@Clinica.pe", Password: "brackets de acero 2026", Name: "Dra. Ana"}, client)
	if err != nil || reg.Token == "" || reg.Account.EmailVerified {
		t.Fatalf("register: %+v %v", reg, err)
	}
	if _, err := f.svc.Register(ctx, application.RegisterInput{Email: "dra.ana@clinica.pe", Password: "otra clave segura 1"}, client); codeOf(err) != "identity.email_taken" {
		t.Fatalf("duplicate: %v", err)
	}

	in, err := f.svc.Login(ctx, "DRA.ANA@clinica.pe", "brackets de acero 2026", client)
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.Authenticate(ctx, in.Token)
	if err != nil || p.Account.Email != "dra.ana@clinica.pe" {
		t.Fatalf("authenticate: %+v %v", p, err)
	}
	if err := f.svc.Logout(ctx, in.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(ctx, in.Token); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatal("logged out token must not authenticate")
	}
}

func TestLoginFailuresLookTheSame(t *testing.T) {
	f := newFixture(t, application.Config{})
	ctx := context.Background()
	_, _ = f.svc.Register(ctx, application.RegisterInput{Email: "ana@clinica.pe", Password: "brackets de acero 2026"}, client)

	_, wrongPassword := f.svc.Login(ctx, "ana@clinica.pe", "incorrecta 12345", client)
	_, unknownEmail := f.svc.Login(ctx, "nadie@clinica.pe", "incorrecta 12345", client)
	_, badEmail := f.svc.Login(ctx, "not-an-email", "incorrecta 12345", client)
	for _, err := range []error{wrongPassword, unknownEmail, badEmail} {
		if codeOf(err) != "identity.credentials_invalid" {
			t.Fatalf("every failure must be the same generic error, got %v", err)
		}
	}
}

func TestSessionExpires(t *testing.T) {
	f := newFixture(t, application.Config{})
	ctx := context.Background()
	reg, _ := f.svc.Register(ctx, application.RegisterInput{Email: "ana@clinica.pe", Password: "brackets de acero 2026"}, client)
	*f.clock = f.clock.Add(domain.SessionTTL + time.Second)
	if _, err := f.svc.Authenticate(ctx, reg.Token); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("expired session: %v", err)
	}
	if len(f.sessions) != 0 {
		t.Fatal("expired session should be deleted")
	}
}

func TestOIDCCreatesAccountAndReusesIt(t *testing.T) {
	f := newFixture(t, application.Config{})
	claims := ports.Claims{Subject: "g-123", Email: "Dr.Luis@gmail.com", EmailVerified: true, Name: "Dr. Luis"}
	first, err := f.googleLogin(t, claims)
	if err != nil || !first.Account.EmailVerified || first.Account.DisplayName != "Dr. Luis" {
		t.Fatalf("first login: %+v %v", first, err)
	}
	claims.Email = "luis.nuevo@gmail.com" // the provider subject is what identifies the user
	second, err := f.googleLogin(t, claims)
	if err != nil || second.Account.ID != first.Account.ID {
		t.Fatalf("second login must reuse the account: %v", err)
	}
	if len(f.accounts.byID) != 1 {
		t.Fatalf("accounts: %d", len(f.accounts.byID))
	}
}

func TestOIDCStateIsSingleUseAndBoundToProvider(t *testing.T) {
	f := newFixture(t, application.Config{})
	ctx := context.Background()
	f.google.claims = ports.Claims{Subject: "g-1", Email: "a@gmail.com", EmailVerified: true}
	_, state, _ := f.svc.StartOIDC(ctx, domain.ProviderGoogle, "http://web/cb", "/")
	if _, _, err := f.svc.FinishOIDC(ctx, domain.ProviderGoogle, "code", state, client); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.svc.FinishOIDC(ctx, domain.ProviderGoogle, "code", state, client); codeOf(err) != "identity.login_state_invalid" {
		t.Fatalf("replayed state: %v", err)
	}
	if _, _, err := f.svc.FinishOIDC(ctx, domain.ProviderGoogle, "code", "forged", client); codeOf(err) != "identity.login_state_invalid" {
		t.Fatalf("forged state: %v", err)
	}

	_, state, _ = f.svc.StartOIDC(ctx, domain.ProviderGoogle, "http://web/cb", "/")
	*f.clock = f.clock.Add(11 * time.Minute)
	if _, _, err := f.svc.FinishOIDC(ctx, domain.ProviderGoogle, "code", state, client); codeOf(err) != "identity.login_state_invalid" {
		t.Fatalf("expired state: %v", err)
	}
	if _, _, err := f.svc.StartOIDC(ctx, domain.ProviderMicrosoft, "http://web/cb", "/"); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("disabled provider: %v", err)
	}
}

func TestOIDCPassesVerifierAndNonceToTheProvider(t *testing.T) {
	f := newFixture(t, application.Config{})
	var gotVerifier, gotNonce string
	f.google.exchange = func(_, verifier, nonce string) error {
		gotVerifier, gotNonce = verifier, nonce
		return nil
	}
	if _, err := f.googleLogin(t, ports.Claims{Subject: "g-1", Email: "a@gmail.com", EmailVerified: true}); err != nil {
		t.Fatal(err)
	}
	if len(gotVerifier) < 43 || gotNonce == "" || !strings.Contains(f.google.lastURL, "nonce="+gotNonce) {
		t.Fatalf("PKCE verifier %q / nonce %q not wired", gotVerifier, gotNonce)
	}

	f.google.exchange = func(string, string, string) error { return errors.New("bad signature") }
	if _, err := f.googleLogin(t, ports.Claims{Subject: "g-1"}); codeOf(err) != "identity.provider_rejected" {
		t.Fatalf("provider failure: %v", err)
	}
}

func TestVerifiedProviderReclaimsAnUnverifiedPasswordAccount(t *testing.T) {
	f := newFixture(t, application.Config{})
	ctx := context.Background()
	// Someone registers with the victim's email and a password they know.
	squat, _ := f.svc.Register(ctx, application.RegisterInput{Email: "victima@gmail.com", Password: "clave del intruso 1"}, client)

	// The real owner signs in with Google (email verified by Google).
	owner, err := f.googleLogin(t, ports.Claims{Subject: "g-victim", Email: "victima@gmail.com", EmailVerified: true})
	if err != nil {
		t.Fatal(err)
	}
	if owner.Account.ID != squat.Account.ID || owner.Account.PasswordHash != "" || !owner.Account.EmailVerified {
		t.Fatalf("account should be reclaimed: %+v", owner.Account)
	}
	if _, err := f.svc.Authenticate(ctx, squat.Token); err == nil {
		t.Fatal("the squatter's session must be revoked")
	}
	if _, err := f.svc.Login(ctx, "victima@gmail.com", "clave del intruso 1", client); err == nil {
		t.Fatal("the squatter's password must no longer work")
	}
}

func TestUnverifiedProviderEmailCannotTakeOverAnAccount(t *testing.T) {
	f := newFixture(t, application.Config{})
	ctx := context.Background()
	_, _ = f.svc.Register(ctx, application.RegisterInput{Email: "ana@clinica.pe", Password: "brackets de acero 2026"}, client)
	_, err := f.googleLogin(t, ports.Claims{Subject: "g-x", Email: "ana@clinica.pe", EmailVerified: false})
	if codeOf(err) != "identity.email_in_use" {
		t.Fatalf("got %v", err)
	}
}

func TestAdminEmailsNeedVerification(t *testing.T) {
	f := newFixture(t, application.Config{AdminEmails: []string{" Jefe@Duradent.pe "}})
	ctx := context.Background()
	reg, _ := f.svc.Register(ctx, application.RegisterInput{Email: "jefe@duradent.pe", Password: "brackets de acero 2026"}, client)
	if reg.Account.Role != domain.RoleCustomer {
		t.Fatal("password sign-up with an admin email must not grant admin")
	}
	in, err := f.googleLogin(t, ports.Claims{Subject: "g-jefe", Email: "jefe@duradent.pe", EmailVerified: true})
	if err != nil || in.Account.Role != domain.RoleAdmin {
		t.Fatalf("verified admin email: %+v %v", in.Account, err)
	}
}

func TestDevLoginOnlyWhenEnabled(t *testing.T) {
	ctx := context.Background()
	off := newFixture(t, application.Config{})
	if _, err := off.svc.DevLogin(ctx, "a@b.pe", "", client); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatalf("dev login must be disabled by default: %v", err)
	}
	on := newFixture(t, application.Config{DevLogin: true, AdminEmails: []string{"admin@duradent.local"}})
	in, err := on.svc.DevLogin(ctx, "admin@duradent.local", "Admin", client)
	if err != nil || in.Account.Role != domain.RoleAdmin {
		t.Fatalf("dev admin: %+v %v", in, err)
	}
	if got := on.svc.Providers(); len(got) != 2 || got[1] != domain.ProviderDev {
		t.Fatalf("providers: %v", got)
	}
}

func TestProfileAddressesAndPriceList(t *testing.T) {
	f := newFixture(t, application.Config{})
	ctx := context.Background()
	ana, _ := f.svc.Register(ctx, application.RegisterInput{Email: "ana@clinica.pe", Password: "brackets de acero 2026"}, client)
	luis, _ := f.svc.Register(ctx, application.RegisterInput{Email: "luis@clinica.pe", Password: "brackets de acero 2026"}, client)

	if err := f.svc.AssignPriceList(ctx, ana.Account.ID, "clinica"); codeOf(err) != "identity.customer_profile_missing" {
		t.Fatalf("price list before profile: %v", err)
	}
	if _, err := f.svc.SaveCustomer(ctx, ana.Account.ID, domain.CustomerInput{Type: domain.CustomerCompany, DocumentType: domain.DocRUC, DocumentNumber: "20131312955", BusinessName: "Clínica Ana SAC"}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AssignPriceList(ctx, ana.Account.ID, "clinica"); err != nil {
		t.Fatal(err)
	}
	// Editing the profile keeps the admin-assigned list.
	if _, err := f.svc.SaveCustomer(ctx, ana.Account.ID, domain.CustomerInput{Type: domain.CustomerCompany, DocumentType: domain.DocRUC, DocumentNumber: "20131312955", BusinessName: "Clínica Ana SAC", Phone: "999888777"}); err != nil {
		t.Fatal(err)
	}
	p, _ := f.svc.Authenticate(ctx, ana.Token)
	if p.PriceListCode != "clinica" {
		t.Fatalf("principal should carry the price list: %+v", p)
	}

	in := domain.AddressInput{Recipient: "Ana", Line1: "Av. Larco 123", District: "Miraflores", Province: "Lima", Department: "15"}
	first, err := f.svc.AddAddress(ctx, ana.Account.ID, in)
	if err != nil || !first.IsDefault {
		t.Fatalf("first address is default: %+v %v", first, err)
	}
	second, _ := f.svc.AddAddress(ctx, ana.Account.ID, in)
	if err := f.svc.RemoveAddress(ctx, luis.Account.ID, first.ID); !apperr.IsKind(err, apperr.KindNotFound) {
		t.Fatal("another customer must not touch Ana's address")
	}
	if err := f.svc.RemoveAddress(ctx, ana.Account.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	prof, _ := f.svc.Profile(ctx, ana.Account.ID)
	if len(prof.Addresses) != 1 || prof.Addresses[0].ID != second.ID || !prof.Addresses[0].IsDefault {
		t.Fatalf("remaining address becomes default: %+v", prof.Addresses)
	}
}

func TestAdminCannotDemoteThemselves(t *testing.T) {
	f := newFixture(t, application.Config{})
	ctx := context.Background()
	a, _ := f.svc.Register(ctx, application.RegisterInput{Email: "a@clinica.pe", Password: "brackets de acero 2026"}, client)
	b, _ := f.svc.Register(ctx, application.RegisterInput{Email: "b@clinica.pe", Password: "brackets de acero 2026"}, client)
	if err := f.svc.SetRole(ctx, a.Account.ID, a.Account.ID, domain.RoleCustomer); codeOf(err) != "identity.self_demotion" {
		t.Fatalf("got %v", err)
	}
	if err := f.svc.SetRole(ctx, a.Account.ID, b.Account.ID, domain.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	got, _ := f.accounts.ByID(ctx, b.Account.ID)
	if got.Role != domain.RoleAdmin {
		t.Fatal("role not saved")
	}
}
