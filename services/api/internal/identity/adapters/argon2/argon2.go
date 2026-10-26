// Package argon2 hashes passwords with Argon2id.
package argon2

import (
	"github.com/alexedwards/argon2id"

	"github.com/CodyLionVivo/duradent/services/api/internal/identity/ports"
)

// OWASP's recommended Argon2id configuration (19 MiB, 2 iterations, 1 lane):
// strong enough, and light enough that a 0.5 GB Fargate task can serve
// concurrent sign-ins.
var params = &argon2id.Params{Memory: 19 * 1024, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32}

type Hasher struct{}

var _ ports.PasswordHasher = Hasher{}

func (Hasher) Hash(password string) (string, error) {
	return argon2id.CreateHash(password, params)
}

// Verify accepts hashes made with older parameters too: they are encoded in
// the hash string itself.
func (Hasher) Verify(password, hash string) (bool, error) {
	return argon2id.ComparePasswordAndHash(password, hash)
}
