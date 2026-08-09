package db

import (
	"errors"
	"testing"
	"time"
)

func mustCredential(t *testing.T, d *DB, userID, credentialID, name string) *WebAuthnCredential {
	t.Helper()
	c, err := d.CreateWebAuthnCredential(NewID(), userID, credentialID, name, `{"id":"stub"}`)
	if err != nil {
		t.Fatalf("CreateWebAuthnCredential(%s): %v", credentialID, err)
	}
	return c
}

func TestWebAuthnCredentialLifecycle(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "pk@example.com")

	created := mustCredential(t, d, u.ID, "cred-a", "Laptop")
	if created.LastUsedAt != 0 {
		t.Errorf("LastUsedAt = %d on a fresh credential, want 0", created.LastUsedAt)
	}

	rows, err := d.WebAuthnCredentialsForUser(u.ID)
	if err != nil {
		t.Fatalf("WebAuthnCredentialsForUser: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "Laptop" || rows[0].CredentialID != "cred-a" {
		t.Fatalf("rows = %+v, want the one credential just created", rows)
	}

	// A sign-in replaces the blob (the signature counter advanced) as well as
	// stamping the time — see TouchWebAuthnCredential's own doc comment.
	if err := d.TouchWebAuthnCredential("cred-a", `{"id":"stub","counter":7}`); err != nil {
		t.Fatalf("TouchWebAuthnCredential: %v", err)
	}
	rows, err = d.WebAuthnCredentialsForUser(u.ID)
	if err != nil {
		t.Fatalf("WebAuthnCredentialsForUser: %v", err)
	}
	if rows[0].Data != `{"id":"stub","counter":7}` {
		t.Errorf("Data = %q, want the updated blob", rows[0].Data)
	}
	if rows[0].LastUsedAt == 0 {
		t.Error("LastUsedAt is still 0 after a touch")
	}

	// Deleting is allowed here because the account also has a password.
	if err := d.DeleteWebAuthnCredential(u.ID, created.ID); err != nil {
		t.Fatalf("DeleteWebAuthnCredential: %v", err)
	}
	if n, err := d.CountWebAuthnCredentials(u.ID); err != nil || n != 0 {
		t.Fatalf("CountWebAuthnCredentials = %d, %v; want 0, nil", n, err)
	}
}

func TestWebAuthnCredentialIDIsUnique(t *testing.T) {
	d := openTest(t)
	a := mustUser(t, d, "a@example.com")
	b := mustUser(t, d, "b@example.com")
	mustCredential(t, d, a.ID, "shared-cred", "First")

	// Same account.
	if _, err := d.CreateWebAuthnCredential(NewID(), a.ID, "shared-cred", "Again", "{}"); !errors.Is(err, ErrCredentialTaken) {
		t.Errorf("err = %v, want ErrCredentialTaken", err)
	}
	// And a different one — a credential id is globally unique, not per-account.
	if _, err := d.CreateWebAuthnCredential(NewID(), b.ID, "shared-cred", "Other", "{}"); !errors.Is(err, ErrCredentialTaken) {
		t.Errorf("cross-account err = %v, want ErrCredentialTaken", err)
	}
}

func TestWebAuthnCredentialsAreScopedToTheAccount(t *testing.T) {
	d := openTest(t)
	a := mustUser(t, d, "a@example.com")
	b := mustUser(t, d, "b@example.com")
	theirs := mustCredential(t, d, b.ID, "cred-b", "Theirs")

	rows, err := d.WebAuthnCredentialsForUser(a.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows = %+v, %v; want none of b's credentials", rows, err)
	}
	// b has a password too, so this is refused for scoping, not for lockout.
	if err := d.DeleteWebAuthnCredential(a.ID, theirs.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-account delete err = %v, want ErrNotFound", err)
	}
	if n, _ := d.CountWebAuthnCredentials(b.ID); n != 1 {
		t.Error("b's credential was deleted by a")
	}
}

func TestDeleteWebAuthnCredentialRefusesToLockOut(t *testing.T) {
	d := openTest(t)
	u, err := d.CreateUser("passkey-only@example.com", "", "n", "EUR") // no password
	if err != nil {
		t.Fatal(err)
	}
	only := mustCredential(t, d, u.ID, "cred-only", "Only key")

	if err := d.DeleteWebAuthnCredential(u.ID, only.ID); !errors.Is(err, ErrLastSignInMethod) {
		t.Fatalf("err = %v, want ErrLastSignInMethod", err)
	}

	// With a password set, removing it is fine.
	if err := d.SetPassword(u.ID, "hash"); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteWebAuthnCredential(u.ID, only.ID); err != nil {
		t.Errorf("delete after setting a password: %v", err)
	}
}

// The guard has to see all three sign-in methods at once. When it only knew
// about passwords and identities, adding passkeys would have let someone delete
// their last identity *and* their last passkey — each check blind to the other,
// each individually looking safe.
func TestCountSignInMethodsSpansAllThree(t *testing.T) {
	d := openTest(t)
	u, err := d.CreateUser("three@example.com", "", "n", "EUR") // no password
	if err != nil {
		t.Fatal(err)
	}

	identity, err := d.CreateIdentity(u.ID, "google", "sub-1", u.Email)
	if err != nil {
		t.Fatal(err)
	}
	cred := mustCredential(t, d, u.ID, "cred-1", "Key")

	// Two methods: an identity and a passkey. Either may go.
	fresh, err := d.UserByID(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := d.countSignInMethods(fresh); err != nil || n != 2 {
		t.Fatalf("countSignInMethods = %d, %v; want 2", n, err)
	}

	// Removing the passkey leaves the identity — allowed.
	if err := d.DeleteWebAuthnCredential(u.ID, cred.ID); err != nil {
		t.Fatalf("delete passkey with an identity present: %v", err)
	}
	// Now the identity is the last way in, from the *other* table — refused.
	if err := d.DeleteIdentity(u.ID, identity.ID); !errors.Is(err, ErrLastSignInMethod) {
		t.Fatalf("delete last identity err = %v, want ErrLastSignInMethod", err)
	}

	// And symmetrically: re-add a passkey, drop the identity, then the passkey
	// is the last way in and must be refused from that side too.
	cred2 := mustCredential(t, d, u.ID, "cred-2", "Key 2")
	if err := d.DeleteIdentity(u.ID, identity.ID); err != nil {
		t.Fatalf("delete identity with a passkey present: %v", err)
	}
	if err := d.DeleteWebAuthnCredential(u.ID, cred2.ID); !errors.Is(err, ErrLastSignInMethod) {
		t.Fatalf("delete last passkey err = %v, want ErrLastSignInMethod", err)
	}
}

func TestWebAuthnSessionIsConsumedOnce(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "s@example.com")
	const id = "session-1"

	if err := d.SaveWebAuthnSession(WebAuthnSession{
		ID: id, UserID: u.ID, Purpose: WebAuthnPurposeRegistration, Data: `{"challenge":"x"}`,
	}, time.Minute); err != nil {
		t.Fatalf("SaveWebAuthnSession: %v", err)
	}

	got, err := d.TakeWebAuthnSession(id)
	if err != nil {
		t.Fatalf("TakeWebAuthnSession: %v", err)
	}
	if got.UserID != u.ID || got.Purpose != WebAuthnPurposeRegistration || got.Data != `{"challenge":"x"}` {
		t.Errorf("session = %+v, want what was saved", got)
	}

	// A challenge must never be answerable twice.
	if _, err := d.TakeWebAuthnSession(id); !errors.Is(err, ErrNotFound) {
		t.Errorf("second take err = %v, want ErrNotFound", err)
	}
}

// A sign-in session has no account attached — the passkey names it later.
func TestWebAuthnSessionAllowsNoUser(t *testing.T) {
	d := openTest(t)
	if err := d.SaveWebAuthnSession(WebAuthnSession{
		ID: "login-1", Purpose: WebAuthnPurposeLogin, Data: "{}",
	}, time.Minute); err != nil {
		t.Fatalf("SaveWebAuthnSession: %v", err)
	}
	got, err := d.TakeWebAuthnSession("login-1")
	if err != nil {
		t.Fatalf("TakeWebAuthnSession: %v", err)
	}
	if got.UserID != "" {
		t.Errorf("UserID = %q, want empty for a sign-in session", got.UserID)
	}
}

func TestWebAuthnSessionExpires(t *testing.T) {
	d := openTest(t)
	if err := d.SaveWebAuthnSession(WebAuthnSession{
		ID: "old", Purpose: WebAuthnPurposeLogin, Data: "{}",
	}, -time.Second); err != nil {
		t.Fatalf("SaveWebAuthnSession: %v", err)
	}
	if _, err := d.TakeWebAuthnSession("old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound for an expired session", err)
	}
}

func TestDeleteExpiredWebAuthnSessions(t *testing.T) {
	d := openTest(t)
	if err := d.SaveWebAuthnSession(WebAuthnSession{ID: "stale", Purpose: WebAuthnPurposeLogin, Data: "{}"}, -time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := d.SaveWebAuthnSession(WebAuthnSession{ID: "live", Purpose: WebAuthnPurposeLogin, Data: "{}"}, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteExpiredWebAuthnSessions(time.Now().Unix()); err != nil {
		t.Fatalf("DeleteExpiredWebAuthnSessions: %v", err)
	}
	if _, err := d.TakeWebAuthnSession("live"); err != nil {
		t.Errorf("the unexpired session was swept: %v", err)
	}
}
