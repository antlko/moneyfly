package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Errorf("hash = %q, want the standard argon2id encoding", hash)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Error("correct password did not verify")
	}
	if VerifyPassword(hash, "correct horse battery stapl") {
		t.Error("wrong password verified")
	}
}

// Every hash gets its own salt, so the same password must never produce the same
// encoding twice.
func TestHashIsSalted(t *testing.T) {
	a, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("same password")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("two hashes of the same password are identical — the salt is not random")
	}
}

// The cost parameters are read back from the hash, so a hash written with
// different parameters must still verify.
func TestVerifyUsesEmbeddedParameters(t *testing.T) {
	// m=8192,t=1,p=1 — deliberately not the current constants.
	const legacy = "$argon2id$v=19$m=8192,t=1,p=1$" +
		"c2FsdHNhbHRzYWx0c2FsdA$" + // "saltsaltsaltsalt"
		"weJ/f5KKsSvvCEmM83RDWjG905dp3xSrsb48l54gqH8"
	if !VerifyPassword(legacy, "hunter2hunter2") {
		t.Error("a hash with older parameters failed to verify")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"",
		"not a hash",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",  // wrong variant
		"$argon2id$v=13$m=65536,t=3,p=2$c2FsdA$aGFzaA", // wrong version
		"$argon2id$v=19$bogus$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$!!!$aGFzaA", // bad base64
	} {
		if VerifyPassword(bad, "anything") {
			t.Errorf("malformed hash %q verified", bad)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword(strings.Repeat("a", MinPasswordLen-1)); err == nil {
		t.Error("short password accepted")
	}
	if err := ValidatePassword(strings.Repeat("a", MaxPasswordLen+1)); err == nil {
		t.Error("overlong password accepted")
	}
	if err := ValidatePassword(strings.Repeat("a", MinPasswordLen)); err != nil {
		t.Errorf("valid password rejected: %v", err)
	}
}

func TestHashTokenIsStableAndNotTheToken(t *testing.T) {
	tok, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 64 {
		t.Errorf("token length = %d, want 64 hex chars", len(tok))
	}
	h := HashToken(tok)
	if h == tok {
		t.Error("HashToken returned the token itself")
	}
	if h != HashToken(tok) {
		t.Error("HashToken is not deterministic")
	}
}
