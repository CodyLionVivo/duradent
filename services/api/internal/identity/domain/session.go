package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/CodyLionVivo/duradent/services/api/internal/kernel/id"
)

// SessionTTL is how long a sign-in lasts. Sessions are opaque random tokens;
// only their SHA-256 is stored, so a database leak does not expose them.
const SessionTTL = 30 * 24 * time.Hour

type Session struct {
	TokenHash string // hex SHA-256 of the token
	AccountID id.ID
	CreatedAt time.Time
	ExpiresAt time.Time
	IP        string
	UserAgent string
}

// NewSession returns the session record and the token to hand to the client.
func NewSession(accountID id.ID, ip, userAgent string, now time.Time) (Session, string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	if len(userAgent) > 255 {
		userAgent = userAgent[:255]
	}
	return Session{
		TokenHash: HashToken(token), AccountID: accountID,
		CreatedAt: now, ExpiresAt: now.Add(SessionTTL), IP: ip, UserAgent: userAgent,
	}, token
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s Session) Expired(now time.Time) bool { return !now.Before(s.ExpiresAt) }
