package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"moneyfly/internal/config"
)

// newTestServer builds a Server on a throwaway config dir. Passing yaml writes a
// config.yaml first, so a test can exercise a non-default instance setting.
func newTestServer(t *testing.T, yaml string) *Server {
	t.Helper()
	dir := t.TempDir()
	if yaml != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// do issues a request against the Fiber app. cookie may be empty.
func (s *Server) do(t *testing.T, method, path, body, cookie string) *http.Response {
	t.Helper()
	return s.doWithHeader(t, method, path, body, "Cookie", cookie)
}

// doWithHeader is do with one arbitrary header instead of a cookie — Bearer
// auth tests need "Authorization", not "Cookie". header is skipped entirely
// when value is empty, the same as do skips an empty cookie.
func (s *Server) doWithHeader(t *testing.T, method, path, body, header, value string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if value != "" {
		req.Header.Set(header, value)
	}
	res, err := s.App().Test(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return res
}

func decodeBody[T any](t *testing.T, res *http.Response) T {
	t.Helper()
	var out T
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

// sessionCookie extracts the session cookie from a response, as "name=value".
func sessionCookie(t *testing.T, res *http.Response) string {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == "moneyfly_session" && c.Value != "" {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatal("no session cookie in response")
	return ""
}

const registerBody = `{"email":"a@example.com","password":"hunter2hunter2","deviceId":"dev-1"}`

func TestRegisterThenMe(t *testing.T) {
	s := newTestServer(t, "")

	res := s.do(t, "POST", "/api/auth/register", registerBody, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d", res.StatusCode)
	}
	user := decodeBody[UserDTO](t, res)
	if user.Email != "a@example.com" || !user.IsAdmin || !user.HasPassword {
		t.Fatalf("registered user = %+v", user)
	}
	cookie := sessionCookie(t, res)

	me := s.do(t, "GET", "/api/auth/me", "", cookie)
	if me.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d", me.StatusCode)
	}
	if got := decodeBody[UserDTO](t, me); got.ID != user.ID {
		t.Errorf("me returned %q, want %q", got.ID, user.ID)
	}
}

func TestMeRequiresSession(t *testing.T) {
	s := newTestServer(t, "")
	if res := s.do(t, "GET", "/api/auth/me", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}
	if res := s.do(t, "GET", "/api/auth/me", "", "moneyfly_session=deadbeef"); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("forged cookie: status = %d, want 401", res.StatusCode)
	}
}

func TestLoginAndLogout(t *testing.T) {
	s := newTestServer(t, "")
	s.do(t, "POST", "/api/auth/register", registerBody, "")

	res := s.do(t, "POST", "/api/auth/login",
		`{"email":"A@Example.com","password":"hunter2hunter2","deviceId":"dev-2"}`, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", res.StatusCode)
	}
	cookie := sessionCookie(t, res)

	if res := s.do(t, "POST", "/api/auth/logout", "", cookie); res.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", res.StatusCode)
	}
	// The session is gone server-side, so the same cookie must stop working.
	if res := s.do(t, "GET", "/api/auth/me", "", cookie); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("after logout: status = %d, want 401", res.StatusCode)
	}
}

// Signing out with no cookie at all must succeed — a client clearing a session
// it no longer has should not see an error.
func TestLogoutWithoutSessionSucceeds(t *testing.T) {
	s := newTestServer(t, "")
	if res := s.do(t, "POST", "/api/auth/logout", "", ""); res.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", res.StatusCode)
	}
}

// Wrong password and unknown account must be indistinguishable, or this endpoint
// tells an attacker who has an account here.
func TestLoginDoesNotLeakAccountExistence(t *testing.T) {
	s := newTestServer(t, "")
	s.do(t, "POST", "/api/auth/register", registerBody, "")

	wrong := s.do(t, "POST", "/api/auth/login",
		`{"email":"a@example.com","password":"wrongwrongwrong"}`, "")
	missing := s.do(t, "POST", "/api/auth/login",
		`{"email":"nobody@example.com","password":"wrongwrongwrong"}`, "")

	if wrong.StatusCode != http.StatusUnauthorized || missing.StatusCode != http.StatusUnauthorized {
		t.Fatalf("statuses = %d and %d, want both 401", wrong.StatusCode, missing.StatusCode)
	}
	a := decodeBody[map[string]string](t, wrong)["error"]
	b := decodeBody[map[string]string](t, missing)["error"]
	if a != b {
		t.Errorf("messages differ: %q vs %q", a, b)
	}
}

func TestLoginRateLimited(t *testing.T) {
	s := newTestServer(t, "")
	s.do(t, "POST", "/api/auth/register", registerBody, "")

	body := `{"email":"a@example.com","password":"wrongwrongwrong"}`
	for range maxFailedLogins {
		s.do(t, "POST", "/api/auth/login", body, "")
	}
	res := s.do(t, "POST", "/api/auth/login", body, "")
	if res.StatusCode != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 after %d failures", res.StatusCode, maxFailedLogins)
	}
}

func TestRegisterRejectsDuplicateAndBadInput(t *testing.T) {
	s := newTestServer(t, "")
	s.do(t, "POST", "/api/auth/register", registerBody, "")

	tests := []struct {
		name string
		body string
		want int
	}{
		{"duplicate email", registerBody, http.StatusConflict},
		{"duplicate different case", `{"email":"A@EXAMPLE.com","password":"hunter2hunter2"}`, http.StatusConflict},
		{"not an email", `{"email":"nope","password":"hunter2hunter2"}`, http.StatusBadRequest},
		{"short password", `{"email":"b@example.com","password":"short"}`, http.StatusBadRequest},
		{"malformed json", `{`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if res := s.do(t, "POST", "/api/auth/register", tt.body, ""); res.StatusCode != tt.want {
				t.Errorf("status = %d, want %d", res.StatusCode, tt.want)
			}
		})
	}
}

// A closed instance must still be claimable by its first user, otherwise nobody
// can ever sign in to it.
func TestClosedRegistrationStillAllowsTheFirstAccount(t *testing.T) {
	s := newTestServer(t, "app:\n  registration: closed\n")

	health := decodeBody[map[string]any](t, s.do(t, "GET", "/api/health", "", ""))
	if health["registrationAllowed"] != true {
		t.Error("an empty closed instance should still offer registration")
	}
	if res := s.do(t, "POST", "/api/auth/register", registerBody, ""); res.StatusCode != http.StatusOK {
		t.Fatalf("first registration status = %d", res.StatusCode)
	}

	second := `{"email":"b@example.com","password":"hunter2hunter2"}`
	if res := s.do(t, "POST", "/api/auth/register", second, ""); res.StatusCode != http.StatusForbidden {
		t.Errorf("second registration status = %d, want 403", res.StatusCode)
	}
	health = decodeBody[map[string]any](t, s.do(t, "GET", "/api/health", "", ""))
	if health["registrationAllowed"] != false {
		t.Error("registration should be closed once an account exists")
	}
}

// Changing the password must invalidate other sessions but keep the caller
// signed in — being bounced to the login screen after a password change is a
// papercut, and worse, it teaches people to distrust the button.
func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	s := newTestServer(t, "")
	first := sessionCookie(t, s.do(t, "POST", "/api/auth/register", registerBody, ""))
	second := sessionCookie(t, s.do(t, "POST", "/api/auth/login",
		`{"email":"a@example.com","password":"hunter2hunter2","deviceId":"dev-2"}`, ""))

	res := s.do(t, "PUT", "/api/auth/password",
		`{"currentPassword":"hunter2hunter2","newPassword":"correct horse battery"}`, first)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("change password status = %d", res.StatusCode)
	}
	renewed := sessionCookie(t, res)

	if r := s.do(t, "GET", "/api/auth/me", "", second); r.StatusCode != http.StatusUnauthorized {
		t.Errorf("the other session survived: status = %d", r.StatusCode)
	}
	if r := s.do(t, "GET", "/api/auth/me", "", renewed); r.StatusCode != http.StatusOK {
		t.Errorf("the caller was signed out: status = %d", r.StatusCode)
	}
	if r := s.do(t, "POST", "/api/auth/login",
		`{"email":"a@example.com","password":"correct horse battery"}`, ""); r.StatusCode != http.StatusOK {
		t.Errorf("login with the new password: status = %d", r.StatusCode)
	}
}

func TestChangePasswordRequiresCurrentOne(t *testing.T) {
	s := newTestServer(t, "")
	cookie := sessionCookie(t, s.do(t, "POST", "/api/auth/register", registerBody, ""))

	res := s.do(t, "PUT", "/api/auth/password",
		`{"currentPassword":"nope","newPassword":"correct horse battery"}`, cookie)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}
}

// Registering records the device the client minted, so the devices screen has
// something to show and sync has an identity to attach cursors to.
func TestDeviceIsRegisteredOnSignIn(t *testing.T) {
	s := newTestServer(t, "")
	cookie := sessionCookie(t, s.do(t, "POST", "/api/auth/register", registerBody, ""))

	devices := decodeBody[[]DeviceDTO](t, s.do(t, "GET", "/api/devices", "", cookie))
	if len(devices) != 1 || devices[0].ID != "dev-1" || !devices[0].Current {
		t.Fatalf("devices = %+v", devices)
	}
}

// Two accounts on one instance must not see each other's devices.
func TestDevicesAreScopedToTheAccount(t *testing.T) {
	s := newTestServer(t, "")
	a := sessionCookie(t, s.do(t, "POST", "/api/auth/register", registerBody, ""))
	b := sessionCookie(t, s.do(t, "POST", "/api/auth/register",
		`{"email":"b@example.com","password":"hunter2hunter2","deviceId":"dev-b"}`, ""))

	for name, cookie := range map[string]string{"a": a, "b": b} {
		devices := decodeBody[[]DeviceDTO](t, s.do(t, "GET", "/api/devices", "", cookie))
		if len(devices) != 1 {
			t.Errorf("account %s sees %d devices, want 1", name, len(devices))
		}
	}
	// And one cannot delete the other's.
	if res := s.do(t, "DELETE", "/api/devices/dev-b", "", a); res.StatusCode != http.StatusNotFound {
		t.Errorf("cross-account delete: status = %d, want 404", res.StatusCode)
	}
}

func TestOIDCStartUnknownProvider(t *testing.T) {
	s := newTestServer(t, "")
	if res := s.do(t, "GET", "/api/auth/oidc/nope/start", "", ""); res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
}

// A bad or replayed state lands the browser back on the sign-in screen with a
// message, never on a JSON error page.
func TestOIDCCallbackWithBadStateRedirects(t *testing.T) {
	s := newTestServer(t, "")
	res := s.do(t, "GET", "/api/auth/oidc/google/callback?state=made-up&code=x", "", "")
	if res.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", res.StatusCode)
	}
	if loc := res.Header.Get("Location"); !strings.HasPrefix(loc, "/signin?error=") {
		t.Errorf("Location = %q", loc)
	}
}

func TestHealthNeverLeaksProviderSecrets(t *testing.T) {
	s := newTestServer(t, `
server:
  base_url: https://money.example.com
oidc:
  - id: google
    name: Google
    issuer: https://accounts.google.com
    client_id: super-secret-client-id
    client_secret: super-secret
`)
	res := s.do(t, "GET", "/api/health", "", "")
	body, _ := io.ReadAll(res.Body)
	if strings.Contains(string(body), "super-secret") {
		t.Fatalf("health leaked provider credentials: %s", body)
	}
	if !strings.Contains(string(body), `"Google"`) {
		t.Errorf("health did not advertise the provider: %s", body)
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	s := newTestServer(t, "")
	res := s.do(t, "GET", "/api/nope", "", "")
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if got := decodeBody[map[string]string](t, res)["error"]; got == "" {
		t.Error("404 body did not use the {\"error\": ...} shape")
	}
}

// Client-side routes must survive a hard refresh.
func TestUnknownPageFallsBackToIndex(t *testing.T) {
	s := newTestServer(t, "")
	res := s.do(t, "GET", "/some/spa/route", "", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want html", ct)
	}
}

// A missing *file* must 404. Falling back to index.html would hand the browser
// an HTML document when it asks for /sw.js, and hide broken builds behind a page
// that renders but does not work.
func TestMissingAssetIsNotTheAppShell(t *testing.T) {
	s := newTestServer(t, "")
	// Names that never exist in either the placeholder dist or a real build —
	// otherwise this test passes or fails depending on whether the SPA has been
	// compiled in, which is exactly the sort of test nobody trusts.
	for _, path := range []string{"/assets/nope.css", "/missing-worker.js", "/not-there.png"} {
		res := s.do(t, "GET", path, "", "")
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want 404", path, res.StatusCode)
		}
		if ct := res.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
			t.Errorf("GET %s: served HTML (%s) instead of a 404", path, ct)
		}
	}
}

// Installability hangs on this one header: served as octet-stream, the manifest
// is ignored and the app cannot be added to a home screen.
func TestManifestHasItsOwnContentType(t *testing.T) {
	s := newTestServer(t, "")
	// The placeholder dist has no manifest, so assert on the mapping rather than
	// on the file: a request that reaches a .webmanifest must not be typed as
	// a generic download.
	res := s.do(t, "GET", "/manifest.webmanifest", "", "")
	if res.StatusCode == http.StatusOK {
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
			t.Errorf("Content-Type = %q, want application/manifest+json", ct)
		}
	} else if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 200 or 404", res.StatusCode)
	}
}

func TestConfigDefaultsAreReachable(t *testing.T) {
	s := newTestServer(t, "")
	health := decodeBody[map[string]any](t, s.do(t, "GET", "/api/health", "", ""))
	if health["defaultCurrency"] != config.DefaultCurrency {
		t.Errorf("defaultCurrency = %v, want %v", health["defaultCurrency"], config.DefaultCurrency)
	}
}
