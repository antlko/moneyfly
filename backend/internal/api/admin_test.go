package api

import (
	"net/http"
	"testing"
)

func TestAdminRoutesRequireAuth(t *testing.T) {
	s := newTestServer(t, "")
	if res := s.do(t, "GET", "/api/admin/users", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET: status = %d, want 401", res.StatusCode)
	}
	if res := s.do(t, "POST", "/api/admin/users", `{"email":"x@example.com","password":"hunter2hunter2"}`, ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST: status = %d, want 401", res.StatusCode)
	}
}

// The first account is always the admin (db.CreateUser); the second never is
// unless promoted, which is exactly the fixture every "non-admin" test here
// wants.
func signUpNonAdmin(t *testing.T, s *Server) (cookie string) {
	t.Helper()
	signUp(t, s, "admin@example.com", "dev-admin") // claims the instance
	return signUp(t, s, "member@example.com", "dev-member")
}

func TestAdminRoutesRequireAdmin(t *testing.T) {
	s := newTestServer(t, "")
	member := signUpNonAdmin(t, s)

	if res := s.do(t, "GET", "/api/admin/users", "", member); res.StatusCode != http.StatusForbidden {
		t.Errorf("GET: status = %d, want 403", res.StatusCode)
	}
	if res := s.do(t, "POST", "/api/admin/users", `{"email":"x@example.com","password":"hunter2hunter2"}`, member); res.StatusCode != http.StatusForbidden {
		t.Errorf("POST: status = %d, want 403", res.StatusCode)
	}
	if res := s.do(t, "DELETE", "/api/admin/users/whatever", "", member); res.StatusCode != http.StatusForbidden {
		t.Errorf("DELETE: status = %d, want 403", res.StatusCode)
	}
	if res := s.do(t, "PUT", "/api/admin/users/whatever", `{"isAdmin":true}`, member); res.StatusCode != http.StatusForbidden {
		t.Errorf("PUT: status = %d, want 403", res.StatusCode)
	}
}

func TestAdminListsEveryUser(t *testing.T) {
	s := newTestServer(t, "")
	admin := signUp(t, s, "admin@example.com", "dev-admin")
	signUp(t, s, "member@example.com", "dev-member")

	list := decodeBody[[]AdminUserDTO](t, s.do(t, "GET", "/api/admin/users", "", admin))
	if len(list) != 2 {
		t.Fatalf("list = %+v, want 2 accounts", list)
	}
}

// The path "root manages the others" exists for: an account provisioned
// directly, with no self-registration involved, that can sign in right away.
func TestAdminCreatesAUserWhoCanThenSignIn(t *testing.T) {
	s := newTestServer(t, "")
	admin := signUp(t, s, "admin@example.com", "dev-admin")

	res := s.do(t, "POST", "/api/admin/users",
		`{"email":"new@example.com","password":"hunter2hunter2","displayName":"New Person"}`, admin)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", res.StatusCode)
	}
	// Provisioning is not signing in: this request is the admin's own
	// session, so nothing here should try to hand the new person a cookie.
	for _, c := range res.Cookies() {
		if c.Name == "moneyfly_session" {
			t.Error("admin-create response set a session cookie")
		}
	}
	created := decodeBody[AdminUserDTO](t, res)
	if created.Email != "new@example.com" || created.IsAdmin {
		t.Fatalf("created = %+v", created)
	}

	login := s.do(t, "POST", "/api/auth/login",
		`{"email":"new@example.com","password":"hunter2hunter2","deviceId":"dev-new"}`, "")
	if login.StatusCode != http.StatusOK {
		t.Fatalf("the admin-created account could not log in: status = %d", login.StatusCode)
	}
}

func TestAdminCreateRejectsDuplicateEmail(t *testing.T) {
	s := newTestServer(t, "")
	admin := signUp(t, s, "admin@example.com", "dev-admin")

	res := s.do(t, "POST", "/api/admin/users",
		`{"email":"admin@example.com","password":"hunter2hunter2"}`, admin)
	if res.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", res.StatusCode)
	}
}

func TestAdminCannotDeleteSelf(t *testing.T) {
	s := newTestServer(t, "")
	admin := signUp(t, s, "admin@example.com", "dev-admin")
	me := decodeBody[UserDTO](t, s.do(t, "GET", "/api/auth/me", "", admin))

	res := s.do(t, "DELETE", "/api/admin/users/"+me.ID, "", admin)
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", res.StatusCode)
	}
}

// Deleting the last admin from someone *else's* request is unreachable
// through this API by construction: reaching handleAdminDeleteUser at all
// requires the caller to be an admin (adminMW), so if the target is "the
// last admin" the caller can only be that same account — which the
// self-delete guard above already refuses, for a clearer reason, before
// DeleteUser's own last-admin check would ever run. That check still exists
// as a db-layer safety net for any future caller that does not carry the
// same guard (TestDeleteUserRefusesTheLastAdmin in internal/db).
//
// Demoting has no self-action guard — stepping down when someone else is
// already admin is reasonable — so this is where the last-admin rule is
// actually reachable over HTTP: the sole admin demoting themselves.
func TestAdminCannotDemoteTheLastAdminViaHTTP(t *testing.T) {
	s := newTestServer(t, "")
	admin := signUp(t, s, "admin@example.com", "dev-admin")
	me := decodeBody[UserDTO](t, s.do(t, "GET", "/api/auth/me", "", admin))

	res := s.do(t, "PUT", "/api/admin/users/"+me.ID, `{"isAdmin":false}`, admin)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", res.StatusCode)
	}
}

func TestSetAdminRejectsUnknownUser(t *testing.T) {
	s := newTestServer(t, "")
	admin := signUp(t, s, "admin@example.com", "dev-admin")

	res := s.do(t, "PUT", "/api/admin/users/nope", `{"isAdmin":true}`, admin)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
}

func TestSetAdminPromotesOverHTTP(t *testing.T) {
	s := newTestServer(t, "")
	admin := signUp(t, s, "admin@example.com", "dev-admin")
	memberCookie := signUp(t, s, "member@example.com", "dev-member")
	member := decodeBody[UserDTO](t, s.do(t, "GET", "/api/auth/me", "", memberCookie))

	res := s.do(t, "PUT", "/api/admin/users/"+member.ID, `{"isAdmin":true}`, admin)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	updated := decodeBody[AdminUserDTO](t, res)
	if !updated.IsAdmin {
		t.Errorf("updated = %+v, want IsAdmin", updated)
	}

	// member is now an admin and can reach the roster.
	list := decodeBody[[]AdminUserDTO](t, s.do(t, "GET", "/api/admin/users", "", memberCookie))
	if len(list) != 2 {
		t.Errorf("promoted member cannot list users: %+v", list)
	}
}
