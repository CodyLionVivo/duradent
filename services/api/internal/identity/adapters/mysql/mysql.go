// Package mysql stores accounts, sessions, customer profiles and addresses.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/identity/domain"
	"github.com/CodyLionVivo/duradent/services/api/internal/identity/ports"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/apperr"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/locale"
	"github.com/CodyLionVivo/duradent/services/api/internal/platform/mysql"
)

type (
	Accounts      struct{ db *sql.DB }
	Customers     struct{ db *sql.DB }
	Addresses     struct{ db *sql.DB }
	Sessions      struct{ db *sql.DB }
	LoginStates   struct{ db *sql.DB }
	Verifications struct{ db *sql.DB }
)

// Stores groups every identity repository over one connection pool.
type Stores struct {
	Accounts      *Accounts
	Customers     *Customers
	Addresses     *Addresses
	Sessions      *Sessions
	LoginStates   *LoginStates
	Verifications *Verifications
}

func New(db *sql.DB) Stores {
	return Stores{&Accounts{db}, &Customers{db}, &Addresses{db}, &Sessions{db}, &LoginStates{db}, &Verifications{db}}
}

var (
	_ ports.AccountRepository      = (*Accounts)(nil)
	_ ports.CustomerRepository     = (*Customers)(nil)
	_ ports.AddressRepository      = (*Addresses)(nil)
	_ ports.SessionRepository      = (*Sessions)(nil)
	_ ports.LoginStateRepository   = (*LoginStates)(nil)
	_ ports.VerificationRepository = (*Verifications)(nil)
)

func notFound(code, msg string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return apperr.NotFound(code, msg)
	}
	return err
}

// --- accounts ------------------------------------------------------------------------

const accountCols = "id, email, email_verified, password_hash, display_name, locale, role, status, created_at, updated_at"

type scanner interface{ Scan(dest ...any) error }

func scanAccount(s scanner) (*domain.Account, error) {
	a := &domain.Account{}
	var l string
	if err := s.Scan(&a.ID, &a.Email, &a.EmailVerified, &a.PasswordHash, &a.DisplayName, &l, &a.Role, &a.Status, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, err
	}
	a.Locale = locale.Locale(l)
	return a, nil
}

func (s *Accounts) Create(ctx context.Context, a *domain.Account, ident *domain.Identity) error {
	// Joins the caller's unit of work, so the CustomerRegistered event and
	// the account commit together.
	err := mysql.NewUnitOfWork(s.db).InTx(ctx, func(ctx context.Context) error {
		tx := mysql.Conn(ctx, s.db)
		if _, err := tx.ExecContext(ctx, "INSERT INTO accounts ("+accountCols+") VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
			a.ID, a.Email, a.EmailVerified, a.PasswordHash, a.DisplayName, string(a.Locale), a.Role, a.Status, a.CreatedAt, a.UpdatedAt); err != nil {
			return err
		}
		if ident != nil {
			return linkIdentity(ctx, tx, a.ID, *ident, a.CreatedAt)
		}
		return nil
	})
	if key, dup := mysql.DuplicateKey(err); dup && key == "uk_accounts_email" {
		return apperr.Conflict("identity.email_taken", "email", "an account with this email already exists")
	}
	return err
}

func (s *Accounts) Update(ctx context.Context, a *domain.Account) error {
	_, err := s.db.ExecContext(ctx, `UPDATE accounts SET email_verified = ?, password_hash = ?, display_name = ?, locale = ?,
		role = ?, status = ?, updated_at = ? WHERE id = ?`,
		a.EmailVerified, a.PasswordHash, a.DisplayName, string(a.Locale), a.Role, a.Status, a.UpdatedAt, a.ID)
	return err
}

func (s *Accounts) ByID(ctx context.Context, accountID id.ID) (*domain.Account, error) {
	a, err := scanAccount(s.db.QueryRowContext(ctx, "SELECT "+accountCols+" FROM accounts WHERE id = ?", accountID))
	return a, notFound("identity.account_not_found", "account not found", err)
}

func (s *Accounts) ByEmail(ctx context.Context, email string) (*domain.Account, error) {
	a, err := scanAccount(s.db.QueryRowContext(ctx, "SELECT "+accountCols+" FROM accounts WHERE email = ?", email))
	return a, notFound("identity.account_not_found", "account not found", err)
}

func (s *Accounts) ByIdentity(ctx context.Context, p domain.Provider, subject string) (*domain.Account, error) {
	a, err := scanAccount(s.db.QueryRowContext(ctx, "SELECT a."+strings.ReplaceAll(accountCols, ", ", ", a.")+
		" FROM accounts a JOIN oauth_identities o ON o.account_id = a.id WHERE o.provider = ? AND o.subject = ?", string(p), subject))
	return a, notFound("identity.account_not_found", "account not found", err)
}

func (s *Accounts) LinkIdentity(ctx context.Context, accountID id.ID, ident domain.Identity) error {
	return mysql.InTx(ctx, s.db, func(tx *sql.Tx) error {
		return linkIdentity(ctx, tx, accountID, ident, time.Now().UTC())
	})
}

func linkIdentity(ctx context.Context, tx mysql.Querier, accountID id.ID, ident domain.Identity, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO oauth_identities (provider, subject, account_id, email, created_at) VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE email = VALUES(email)`, string(ident.Provider), ident.Subject, accountID, ident.Email, now)
	return err
}

func (s *Accounts) List(ctx context.Context, query string, limit, offset int) ([]*domain.Account, int, error) {
	where, args := "", []any{}
	if query != "" {
		like := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query) + "%"
		where = ` WHERE a.email LIKE ? OR a.display_name LIKE ? OR EXISTS (SELECT 1 FROM customers c
			WHERE c.account_id = a.id AND (c.document_number LIKE ? OR c.business_name LIKE ?))`
		args = []any{like, like, like, like}
	}
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM accounts a"+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT a."+strings.ReplaceAll(accountCols, ", ", ", a.")+" FROM accounts a"+where+
		" ORDER BY a.created_at DESC LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

// --- customers -----------------------------------------------------------------------

const customerCols = "account_id, customer_type, document_type, document_number, business_name, cop_number, phone, price_list_code, updated_at"

func scanCustomer(s scanner) (*domain.Customer, error) {
	c := &domain.Customer{}
	err := s.Scan(&c.AccountID, &c.Type, &c.DocumentType, &c.DocumentNumber, &c.BusinessName, &c.COPNumber, &c.Phone, &c.PriceListCode, &c.UpdatedAt)
	return c, err
}

func (s *Customers) Save(ctx context.Context, c *domain.Customer) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO customers ("+customerCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE customer_type = VALUES(customer_type), document_type = VALUES(document_type),
			document_number = VALUES(document_number), business_name = VALUES(business_name), cop_number = VALUES(cop_number),
			phone = VALUES(phone), price_list_code = VALUES(price_list_code), updated_at = VALUES(updated_at)`,
		c.AccountID, c.Type, c.DocumentType, c.DocumentNumber, c.BusinessName, c.COPNumber, c.Phone, c.PriceListCode, c.UpdatedAt)
	return err
}

func (s *Customers) ByAccount(ctx context.Context, accountID id.ID) (*domain.Customer, error) {
	c, err := scanCustomer(s.db.QueryRowContext(ctx, "SELECT "+customerCols+" FROM customers WHERE account_id = ?", accountID))
	if err != nil {
		return nil, notFound("identity.customer_not_found", "no billing profile", err)
	}
	return c, nil
}

func (s *Customers) ByAccounts(ctx context.Context, accountIDs []id.ID) (map[id.ID]*domain.Customer, error) {
	out := map[id.ID]*domain.Customer{}
	if len(accountIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(accountIDs))
	for i, v := range accountIDs {
		args[i] = v
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+customerCols+" FROM customers WHERE account_id IN ("+mysql.Placeholders(len(args))+")", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, err
		}
		out[c.AccountID] = c
	}
	return out, rows.Err()
}

// --- addresses -----------------------------------------------------------------------

const addressCols = "id, account_id, label, recipient, line1, reference, district, province, department, ubigeo, phone, is_default, created_at"

func (s *Addresses) List(ctx context.Context, accountID id.ID) ([]*domain.Address, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+addressCols+" FROM addresses WHERE account_id = ? ORDER BY is_default DESC, created_at", accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*domain.Address
	for rows.Next() {
		a := &domain.Address{}
		if err := rows.Scan(&a.ID, &a.AccountID, &a.Label, &a.Recipient, &a.Line1, &a.Reference, &a.District, &a.Province,
			&a.Department, &a.Ubigeo, &a.Phone, &a.IsDefault, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Addresses) Save(ctx context.Context, a *domain.Address) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO addresses ("+addressCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE label = VALUES(label), recipient = VALUES(recipient), line1 = VALUES(line1),
			reference = VALUES(reference), district = VALUES(district), province = VALUES(province),
			department = VALUES(department), ubigeo = VALUES(ubigeo), phone = VALUES(phone), is_default = VALUES(is_default)`,
		a.ID, a.AccountID, a.Label, a.Recipient, a.Line1, a.Reference, a.District, a.Province, a.Department, a.Ubigeo, a.Phone, a.IsDefault, a.CreatedAt)
	return err
}

func (s *Addresses) Delete(ctx context.Context, accountID, addressID id.ID) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM addresses WHERE id = ? AND account_id = ?", addressID, accountID)
	return err
}

func (s *Addresses) SetDefault(ctx context.Context, accountID, addressID id.ID) error {
	_, err := s.db.ExecContext(ctx, "UPDATE addresses SET is_default = (id = ?) WHERE account_id = ?", addressID, accountID)
	return err
}

// --- sessions ------------------------------------------------------------------------

func (s *Sessions) Create(ctx context.Context, sess domain.Session) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO sessions (token_hash, account_id, created_at, expires_at, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?)",
		sess.TokenHash, sess.AccountID, sess.CreatedAt, sess.ExpiresAt, sess.IP, sess.UserAgent)
	return err
}

func (s *Sessions) ByTokenHash(ctx context.Context, hash string) (domain.Session, error) {
	var sess domain.Session
	err := s.db.QueryRowContext(ctx, "SELECT token_hash, account_id, created_at, expires_at, ip, user_agent FROM sessions WHERE token_hash = ?", hash).
		Scan(&sess.TokenHash, &sess.AccountID, &sess.CreatedAt, &sess.ExpiresAt, &sess.IP, &sess.UserAgent)
	return sess, notFound("identity.session_not_found", "session not found", err)
}

func (s *Sessions) Delete(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash = ?", hash)
	return err
}

func (s *Sessions) DeleteForAccount(ctx context.Context, accountID id.ID) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE account_id = ?", accountID)
	return err
}

// --- login states --------------------------------------------------------------------

func (s *LoginStates) Save(ctx context.Context, ls ports.LoginState) error {
	// Opportunistic cleanup keeps the table tiny without a background job.
	if _, err := s.db.ExecContext(ctx, "DELETE FROM login_states WHERE expires_at < ?", time.Now().UTC().Add(-time.Hour)); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO login_states (state, provider, nonce, verifier, redirect_uri, return_to, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		ls.State, string(ls.Provider), ls.Nonce, ls.Verifier, ls.RedirectURI, ls.ReturnTo, ls.ExpiresAt)
	return err
}

// Take deletes the row in the same transaction it reads it, so a state can be
// redeemed only once even under concurrent callbacks.
func (s *LoginStates) Take(ctx context.Context, state string) (ports.LoginState, error) {
	var ls ports.LoginState
	err := mysql.InTx(ctx, s.db, func(tx *sql.Tx) error {
		var p string
		err := tx.QueryRowContext(ctx, "SELECT state, provider, nonce, verifier, redirect_uri, return_to, expires_at FROM login_states WHERE state = ? FOR UPDATE", state).
			Scan(&ls.State, &p, &ls.Nonce, &ls.Verifier, &ls.RedirectURI, &ls.ReturnTo, &ls.ExpiresAt)
		if err != nil {
			return notFound("identity.state_not_found", "login state not found", err)
		}
		ls.Provider = domain.Provider(p)
		_, err = tx.ExecContext(ctx, "DELETE FROM login_states WHERE state = ?", state)
		return err
	})
	return ls, err
}

// --- email verifications ---------------------------------------------------------------

func (s *Verifications) Create(ctx context.Context, v ports.Verification) error {
	_, err := mysql.Conn(ctx, s.db).ExecContext(ctx,
		"INSERT INTO email_verifications (token_hash, account_id, email, expires_at, created_at) VALUES (?, ?, ?, ?, ?)",
		v.TokenHash, v.AccountID, v.Email, v.ExpiresAt, v.CreatedAt)
	return err
}

func (s *Verifications) Take(ctx context.Context, tokenHash string) (ports.Verification, error) {
	var v ports.Verification
	err := mysql.InTx(ctx, s.db, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT token_hash, account_id, email, expires_at, created_at FROM email_verifications WHERE token_hash = ? FOR UPDATE", tokenHash).
			Scan(&v.TokenHash, &v.AccountID, &v.Email, &v.ExpiresAt, &v.CreatedAt)
		if err != nil {
			return notFound("identity.verification_not_found", "verification not found", err)
		}
		// One link confirms once; older links of the account die with it.
		_, err = tx.ExecContext(ctx, "DELETE FROM email_verifications WHERE account_id = ?", v.AccountID)
		return err
	})
	return v, err
}

func (s *Verifications) Recent(ctx context.Context, accountID id.ID, since time.Time) (int, error) {
	var n int
	err := mysql.Conn(ctx, s.db).QueryRowContext(ctx, "SELECT COUNT(*) FROM email_verifications WHERE account_id = ? AND created_at >= ?", accountID, since).Scan(&n)
	return n, err
}
