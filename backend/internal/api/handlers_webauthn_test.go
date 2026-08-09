package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/descope/virtualwebauthn"
)

// passkeyConfig is the minimum an instance needs for passkeys to be offered: a
// public URL, which is where the relying-party id comes from.
const passkeyConfig = "server:\n  base_url: https://money.example.test\n"

// The four ceremony routes, for the tests that assert all of them behave the
// same way about a precondition.
var ceremonyRoutes = []string{
	"/api/auth/webauthn/register/options",
	"/api/auth/webauthn/register/verify",
	"/api/auth/webauthn/login/options",
	"/api/auth/webauthn/login/verify",
}

func TestWebAuthnDisabledWithoutBaseURL(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	health := decodeBody[map[string]any](t, s.do(t, "GET", "/api/health", "", ""))
	if health["webauthnEnabled"] != false {
		t.Errorf("webauthnEnabled = %v, want false with no base_url", health["webauthnEnabled"])
	}

	for _, path := range ceremonyRoutes {
		res := s.do(t, "POST", path, "{}", cookie)
		if res.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("POST %s: status = %d, want 503", path, res.StatusCode)
		}
	}

	// Management keeps working regardless: an already-registered passkey must
	// stay visible and removable even if base_url is later unset.
	if res := s.do(t, "GET", "/api/auth/webauthn/credentials", "", cookie); res.StatusCode != http.StatusOK {
		t.Errorf("list credentials: status = %d, want 200", res.StatusCode)
	}
	if res := s.do(t, "DELETE", "/api/auth/webauthn/credentials/nope", "", cookie); res.StatusCode != http.StatusNotFound {
		t.Errorf("delete unknown credential: status = %d, want 404", res.StatusCode)
	}
}

func TestWebAuthnEnabledWithBaseURL(t *testing.T) {
	s := newTestServer(t, passkeyConfig)
	health := decodeBody[map[string]any](t, s.do(t, "GET", "/api/health", "", ""))
	if health["webauthnEnabled"] != true {
		t.Errorf("webauthnEnabled = %v, want true", health["webauthnEnabled"])
	}
}

// A malformed base_url disables passkeys rather than stopping the instance from
// booting — password sign-in must survive a typo in an unrelated field.
func TestWebAuthnDisabledByMalformedBaseURL(t *testing.T) {
	s := newTestServer(t, "server:\n  base_url: \"::not a url\"\n")
	health := decodeBody[map[string]any](t, s.do(t, "GET", "/api/health", "", ""))
	if health["webauthnEnabled"] != false {
		t.Errorf("webauthnEnabled = %v, want false for an unparseable base_url", health["webauthnEnabled"])
	}
	if health["status"] != "ok" {
		t.Error("the instance did not come up healthy")
	}
}

func TestWebAuthnRegistrationRequiresAuth(t *testing.T) {
	s := newTestServer(t, passkeyConfig)
	for _, path := range []string{
		"/api/auth/webauthn/register/options",
		"/api/auth/webauthn/register/verify",
	} {
		if res := s.do(t, "POST", path, "{}", ""); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("POST %s with no cookie: status = %d, want 401", path, res.StatusCode)
		}
	}
	if res := s.do(t, "GET", "/api/auth/webauthn/credentials", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("list with no cookie: status = %d, want 401", res.StatusCode)
	}
}

// Sign-in options are public and must reveal nothing: the response is the same
// whether or not any account or passkey exists.
func TestWebAuthnLoginOptionsIsPublic(t *testing.T) {
	s := newTestServer(t, passkeyConfig)

	res := s.do(t, "POST", "/api/auth/webauthn/login/options", "", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	body := decodeBody[map[string]json.RawMessage](t, res)
	if _, ok := body["sessionId"]; !ok {
		t.Error("no sessionId in the response")
	}
	publicKey := decodeInner[map[string]any](t, body["publicKey"])
	if publicKey["challenge"] == nil {
		t.Errorf("no challenge in publicKey: %v", publicKey)
	}
	// Discoverable: naming credentials would tell an anonymous caller which
	// ones this origin knows about.
	if _, ok := publicKey["allowCredentials"]; ok {
		t.Error("publicKey carries allowCredentials; sign-in must be discoverable")
	}
}

func TestWebAuthnVerifyRejectsAnUnknownSession(t *testing.T) {
	s := newTestServer(t, passkeyConfig)
	cookie := signUp(t, s, "a@example.com", "dev-a")
	body := `{"sessionId":"made-up","credential":{}}`

	if res := s.do(t, "POST", "/api/auth/webauthn/register/verify", body, cookie); res.StatusCode != http.StatusBadRequest {
		t.Errorf("register/verify: status = %d, want 400", res.StatusCode)
	}
	if res := s.do(t, "POST", "/api/auth/webauthn/login/verify", body, ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("login/verify: status = %d, want 401", res.StatusCode)
	}
}

// A registration challenge belongs to the account that started it.
func TestWebAuthnRegistrationSessionIsBoundToItsAccount(t *testing.T) {
	s := newTestServer(t, passkeyConfig)
	a := signUp(t, s, "a@example.com", "dev-a")
	b := signUp(t, s, "b@example.com", "dev-b")

	opts := decodeBody[map[string]json.RawMessage](t,
		s.do(t, "POST", "/api/auth/webauthn/register/options", "", a))
	var sessionID string
	if err := json.Unmarshal(opts["sessionId"], &sessionID); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]any{"sessionId": sessionID, "credential": map[string]any{}})
	if res := s.do(t, "POST", "/api/auth/webauthn/register/verify", string(body), b); res.StatusCode != http.StatusBadRequest {
		t.Errorf("finishing a's ceremony as b: status = %d, want 400", res.StatusCode)
	}
}

// The real thing: a software authenticator answers the actual challenges with
// cryptographically valid responses, so this drives the same signature
// verification a browser would — not just the malformed-input paths above.
func TestWebAuthnRegisterAndSignInRoundTrip(t *testing.T) {
	s := newTestServer(t, passkeyConfig)
	cookie := signUp(t, s, "passkey@example.com", "dev-a")

	rp := virtualwebauthn.RelyingParty{
		Name:   "moneyfly",
		ID:     "money.example.test",
		Origin: "https://money.example.test",
	}
	cred := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)

	// --- Registration -----------------------------------------------------
	optsRes := decodeBody[map[string]json.RawMessage](t,
		s.do(t, "POST", "/api/auth/webauthn/register/options", "", cookie))
	var sessionID string
	if err := json.Unmarshal(optsRes["sessionId"], &sessionID); err != nil {
		t.Fatal(err)
	}
	attOpts, err := virtualwebauthn.ParseAttestationOptions(string(optsRes["publicKey"]))
	if err != nil {
		t.Fatalf("ParseAttestationOptions: %v", err)
	}

	// The authenticator has to remember the user handle the options carried:
	// a discoverable sign-in later hands that back, and it is the *only* thing
	// naming the account — exactly what a real platform authenticator stores
	// alongside a resident credential. ParseAttestationOptions has already
	// base64url-decoded it, so UserID holds the raw account UUID bytes.
	authenticator := virtualwebauthn.NewAuthenticatorWithOptions(
		virtualwebauthn.AuthenticatorOptions{UserHandle: []byte(attOpts.UserID)})

	attResp := virtualwebauthn.CreateAttestationResponse(rp, authenticator, cred, *attOpts)

	verifyBody, _ := json.Marshal(map[string]any{
		"sessionId":  sessionID,
		"name":       "Test key",
		"credential": json.RawMessage(attResp),
	})
	res := s.do(t, "POST", "/api/auth/webauthn/register/verify", string(verifyBody), cookie)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("register/verify: status = %d, want 201", res.StatusCode)
	}
	registered := decodeBody[WebAuthnCredentialDTO](t, res)
	if registered.Name != "Test key" || registered.ID == "" {
		t.Errorf("registered = %+v, want the named credential", registered)
	}
	authenticator.AddCredential(cred)

	// It shows up on the account, and the account now reports having one.
	list := decodeBody[[]WebAuthnCredentialDTO](t,
		s.do(t, "GET", "/api/auth/webauthn/credentials", "", cookie))
	if len(list) != 1 || list[0].ID != registered.ID {
		t.Fatalf("list = %+v, want the one just registered", list)
	}
	me := decodeBody[UserDTO](t, s.do(t, "GET", "/api/auth/me", "", cookie))
	if !me.HasPasskey {
		t.Error("hasPasskey is false after registering one")
	}

	// --- Sign-in, usernameless and with no existing session ---------------
	loginOpts := decodeBody[map[string]json.RawMessage](t,
		s.do(t, "POST", "/api/auth/webauthn/login/options", "", ""))
	var loginSessionID string
	if err := json.Unmarshal(loginOpts["sessionId"], &loginSessionID); err != nil {
		t.Fatal(err)
	}
	assOpts, err := virtualwebauthn.ParseAssertionOptions(string(loginOpts["publicKey"]))
	if err != nil {
		t.Fatalf("ParseAssertionOptions: %v", err)
	}
	assResp := virtualwebauthn.CreateAssertionResponse(rp, authenticator, cred, *assOpts)

	loginBody, _ := json.Marshal(map[string]any{
		"sessionId":  loginSessionID,
		"credential": json.RawMessage(assResp),
		"deviceId":   "dev-passkey",
		"platform":   "web",
	})
	loginRes := s.do(t, "POST", "/api/auth/webauthn/login/verify", string(loginBody), "")
	if loginRes.StatusCode != http.StatusOK {
		t.Fatalf("login/verify: status = %d, want 200", loginRes.StatusCode)
	}
	// A real session, resolved to the right account purely from the credential.
	if c := sessionCookie(t, loginRes); c == "" {
		t.Error("no session cookie issued by a passkey sign-in")
	}
	signedIn := decodeBody[UserDTO](t, loginRes)
	if signedIn.Email != "passkey@example.com" {
		t.Errorf("signed in as %q, want passkey@example.com", signedIn.Email)
	}

	// The challenge is single-use: replaying the exact same assertion fails.
	replay := s.do(t, "POST", "/api/auth/webauthn/login/verify", string(loginBody), "")
	if replay.StatusCode != http.StatusUnauthorized {
		t.Errorf("replayed assertion: status = %d, want 401", replay.StatusCode)
	}
}

// Removing a passkey is refused while it is the only way into the account, and
// allowed once it is not — the guard spanning all three sign-in methods.
func TestWebAuthnDeleteRefusesTheLastSignInMethod(t *testing.T) {
	s := newTestServer(t, passkeyConfig)
	cookie := signUp(t, s, "solo@example.com", "dev-a")
	user := decodeBody[UserDTO](t, s.do(t, "GET", "/api/auth/me", "", cookie))

	// Register a passkey directly through the store: this test is about the
	// delete guard, not the ceremony (which the round trip above covers).
	row, err := s.conn().CreateWebAuthnCredential(
		"cred-row-1", user.ID, "cred-key-1", "Only key", `{"id":"stub"}`)
	if err != nil {
		t.Fatal(err)
	}

	// The account still has its password, so removal is fine.
	if res := s.do(t, "DELETE", "/api/auth/webauthn/credentials/"+row.ID, "", cookie); res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete with a password present: status = %d, want 204", res.StatusCode)
	}

	// Now strip the password and leave only a passkey: removal must be refused.
	if _, err := s.conn().CreateWebAuthnCredential("cred-row-2", user.ID, "cred-key-2", "Only key", `{"id":"stub"}`); err != nil {
		t.Fatal(err)
	}
	if err := s.conn().SetPassword(user.ID, ""); err != nil {
		t.Fatal(err)
	}
	res := s.do(t, "DELETE", "/api/auth/webauthn/credentials/cred-row-2", "", cookie)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("delete the only sign-in method: status = %d, want 409", res.StatusCode)
	}
}

// decodeInner unmarshals a nested raw message, for responses whose fields are
// themselves objects.
func decodeInner[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode nested json: %v", err)
	}
	return out
}
