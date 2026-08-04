package db

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// openTest gives each test its own on-disk database. A file rather than
// :memory: because Open sets MaxOpenConns(1) and enables WAL, and we want the
// tests running against the same configuration production does.
func openTest(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func mustUser(t *testing.T, d *DB, email string) *User {
	t.Helper()
	u, err := d.CreateUser(email, "hash", "Name", "EUR")
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", email, err)
	}
	return u
}

func TestMigrationsRunOnOpen(t *testing.T) {
	d := openTest(t)
	if n, err := d.CountUsers(); err != nil || n != 0 {
		t.Fatalf("CountUsers = %d, %v; want 0, nil", n, err)
	}
}

func TestCreateUserFirstIsAdmin(t *testing.T) {
	d := openTest(t)

	first := mustUser(t, d, "first@example.com")
	if !first.IsAdmin {
		t.Error("the first account should be the admin")
	}
	second := mustUser(t, d, "second@example.com")
	if second.IsAdmin {
		t.Error("the second account should not be an admin")
	}
}

// Email matching is case-insensitive, so registration must be too — otherwise
// Bob@x and bob@x become two accounts that both "already exist" at sign-in.
func TestEmailIsCaseInsensitive(t *testing.T) {
	d := openTest(t)
	mustUser(t, d, "Bob@Example.com")

	if _, err := d.CreateUser("bob@example.com", "h", "n", "EUR"); !errors.Is(err, ErrEmailTaken) {
		t.Errorf("CreateUser with a different case: err = %v, want ErrEmailTaken", err)
	}
	if _, err := d.UserByEmail("BOB@EXAMPLE.COM"); err != nil {
		t.Errorf("UserByEmail with a different case: %v", err)
	}
}

func TestUserNotFound(t *testing.T) {
	d := openTest(t)
	if _, err := d.UserByEmail("nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	if err := d.CreateSession("hash1", u.ID, "dev1", "agent", time.Hour); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, sess, err := d.SessionUser("hash1")
	if err != nil {
		t.Fatalf("SessionUser: %v", err)
	}
	if got.ID != u.ID || sess.DeviceID != "dev1" {
		t.Errorf("session resolved to user %q device %q", got.ID, sess.DeviceID)
	}

	if err := d.DeleteSession("hash1"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, _, err := d.SessionUser("hash1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: err = %v, want ErrNotFound", err)
	}
}

// An expired row is present but must read as absent, and retention must remove it.
func TestExpiredSessionIsNotValid(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	if err := d.CreateSession("expired", u.ID, "", "", -time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, _, err := d.SessionUser("expired"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired session: err = %v, want ErrNotFound", err)
	}
	if err := d.DeleteExpiredSessions(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d expired sessions survived retention", n)
	}
}

func TestIdentityLinkAndLookup(t *testing.T) {
	d := openTest(t)
	u := mustUser(t, d, "a@example.com")

	if _, err := d.CreateIdentity(u.ID, "google", "sub-1", "a@example.com"); err != nil {
		t.Fatalf("CreateIdentity: %v", err)
	}
	got, err := d.IdentityBySubject("google", "sub-1")
	if err != nil || got.UserID != u.ID {
		t.Fatalf("IdentityBySubject = %+v, %v", got, err)
	}

	// The same subject must not be linkable to a second account.
	other := mustUser(t, d, "b@example.com")
	if _, err := d.CreateIdentity(other.ID, "google", "sub-1", "b@example.com"); !errors.Is(err, ErrIdentityTaken) {
		t.Errorf("relinking a subject: err = %v, want ErrIdentityTaken", err)
	}
}

// Removing the only way into an account would strand it: there is no support
// desk on a self-hosted instance.
func TestDeleteIdentityRefusesToLockOut(t *testing.T) {
	d := openTest(t)
	u, err := d.CreateUser("oidc-only@example.com", "", "n", "EUR") // no password
	if err != nil {
		t.Fatal(err)
	}
	id, err := d.CreateIdentity(u.ID, "google", "sub-1", u.Email)
	if err != nil {
		t.Fatal(err)
	}

	if err := d.DeleteIdentity(u.ID, id.ID); !errors.Is(err, ErrLastSignInMethod) {
		t.Fatalf("err = %v, want ErrLastSignInMethod", err)
	}

	// With a password set, unlinking is fine.
	if err := d.SetPassword(u.ID, "hash"); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteIdentity(u.ID, id.ID); err != nil {
		t.Errorf("unlink after setting a password: %v", err)
	}
}

// A device id is minted by the client, so a second account must not be able to
// claim one by guessing it.
func TestUpsertDeviceCannotBeStolen(t *testing.T) {
	d := openTest(t)
	owner := mustUser(t, d, "owner@example.com")
	thief := mustUser(t, d, "thief@example.com")

	if err := d.UpsertDevice(owner.ID, "dev-1", "Phone", "ios"); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertDevice(thief.ID, "dev-1", "Stolen", "web"); err != nil {
		t.Fatalf("UpsertDevice by another user should be a no-op, got: %v", err)
	}

	devices, err := d.DevicesForUser(owner.ID)
	if err != nil || len(devices) != 1 || devices[0].Name != "Phone" {
		t.Fatalf("owner devices = %+v, %v", devices, err)
	}
	if devices, _ := d.DevicesForUser(thief.ID); len(devices) != 0 {
		t.Errorf("thief acquired %d devices", len(devices))
	}
}

// State must be single-use even when the callback arrives twice.
func TestOIDCStateIsConsumedOnce(t *testing.T) {
	d := openTest(t)
	st := OIDCState{State: "s1", Provider: "google", Nonce: "n", CodeVerifier: "v", RedirectTo: "/"}
	if err := d.SaveOIDCState(st, time.Minute); err != nil {
		t.Fatal(err)
	}

	got, err := d.TakeOIDCState("s1")
	if err != nil || got.Nonce != "n" {
		t.Fatalf("TakeOIDCState = %+v, %v", got, err)
	}
	if _, err := d.TakeOIDCState("s1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("replayed state: err = %v, want ErrNotFound", err)
	}
}

// An expired state is deleted as well as refused, so it cannot be retried.
func TestOIDCStateExpires(t *testing.T) {
	d := openTest(t)
	st := OIDCState{State: "old", Provider: "google", Nonce: "n", CodeVerifier: "v", RedirectTo: "/"}
	if err := d.SaveOIDCState(st, -time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TakeOIDCState("old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM oidc_state`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("an expired state survived being taken")
	}
}

func TestNewIDIsSortableAndUnique(t *testing.T) {
	seen := make(map[string]bool, 100)
	prev := ""
	for range 100 {
		id := NewID()
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
		if id < prev {
			t.Fatalf("id %q sorts before the previous %q — v7 ids must be ordered", id, prev)
		}
		prev = id
	}
}
