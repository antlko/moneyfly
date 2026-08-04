// Package auth provides password hashing, opaque session tokens and the OIDC
// client. Cookies themselves are set and cleared by the API layer.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	// MinPasswordLen is the shortest password accepted on registration.
	MinPasswordLen = 8
	// MaxPasswordLen bounds the input so a huge body cannot turn a login into a
	// denial of service. Argon2 has no 72-byte truncation problem like bcrypt,
	// so this is a sanity limit rather than a correctness one.
	MaxPasswordLen = 1024
)

// Argon2id parameters (RFC 9106's second recommended option: 64 MiB, 3 passes).
// They are encoded into every hash, so raising them later still verifies old
// passwords — only new hashes use the new cost.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// ErrInvalidHash is returned when a stored hash is not a recognised argon2id
// encoding — i.e. the row is corrupt, not the password wrong.
var ErrInvalidHash = errors.New("auth: malformed password hash")

// HashPassword returns an argon2id hash in the standard encoded form:
//
//	$argon2id$v=19$m=65536,t=3,p=2$<salt>$<hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64(salt), b64(key)), nil
}

// VerifyPassword reports whether password matches the encoded hash. It derives
// the cost parameters from the hash itself, so hashes written by an older (or
// newer) parameter set still verify.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ValidatePassword checks a candidate password before it is hashed.
func ValidatePassword(password string) error {
	switch {
	case len(password) < MinPasswordLen:
		return fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	case len(password) > MaxPasswordLen:
		return fmt.Errorf("password must be at most %d characters", MaxPasswordLen)
	}
	return nil
}
