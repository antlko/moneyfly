package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// CookieName is the session cookie name.
const CookieName = "moneyfly_session"

// NewToken returns a cryptographically random 256-bit token, hex encoded.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: read random: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// HashToken returns the value stored for a session or API token.
//
// A plain SHA-256 is the right primitive here, unlike for passwords: the token
// is 256 bits of uniform randomness, so there is no dictionary to attack and a
// deliberately slow hash would only tax every authenticated request.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewURLToken returns a random URL-safe string, used for OIDC state, nonce and
// PKCE verifier values.
func NewURLToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
