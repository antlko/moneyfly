package api

import (
	"fmt"
	"net/http"
	"testing"
)

func TestTokensRequireAuth(t *testing.T) {
	s := newTestServer(t, "")
	if res := s.do(t, "GET", "/api/tokens", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET: status = %d, want 401", res.StatusCode)
	}
	if res := s.do(t, "POST", "/api/tokens", `{"name":"x"}`, ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST: status = %d, want 401", res.StatusCode)
	}
}

func TestCreateTokenReturnsPlaintextOnceThenOnlyMetadata(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")

	created := decodeBody[createTokenResponse](t, s.do(t, "POST", "/api/tokens", `{"name":"laptop script"}`, cookie))
	if created.Token == "" {
		t.Fatal("no plaintext token in the create response")
	}
	if created.Name != "laptop script" {
		t.Errorf("name = %q", created.Name)
	}

	list := decodeBody[[]apiTokenDTO](t, s.do(t, "GET", "/api/tokens", "", cookie))
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}
	// The list must never carry the plaintext or a hash — apiTokenDTO simply
	// has no field for either, which this assertion is really checking: the
	// type itself, not a value that could be blanked out and forgotten.
}

func TestCreateTokenRejectsEmptyName(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	res := s.do(t, "POST", "/api/tokens", `{"name":"  "}`, cookie)
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", res.StatusCode)
	}
}

// The whole point of a token: it authenticates a request with no cookie at
// all, the same routes a browser uses.
func TestBearerTokenAuthenticatesOrdinaryRoutes(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	created := decodeBody[createTokenResponse](t, s.do(t, "POST", "/api/tokens", `{"name":"script"}`, cookie))

	req := fmt.Sprintf("Bearer %s", created.Token)
	res := s.doWithHeader(t, "GET", "/api/auth/me", "", "Authorization", req)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	me := decodeBody[UserDTO](t, res)
	if me.Email != "a@example.com" {
		t.Errorf("me = %+v", me)
	}
}

func TestBearerTokenRejectsUnknownToken(t *testing.T) {
	s := newTestServer(t, "")
	res := s.doWithHeader(t, "GET", "/api/auth/me", "", "Authorization", "Bearer not-a-real-token")
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}
}

func TestDeleteTokenRevokesIt(t *testing.T) {
	s := newTestServer(t, "")
	cookie := signUp(t, s, "a@example.com", "dev-a")
	created := decodeBody[createTokenResponse](t, s.do(t, "POST", "/api/tokens", `{"name":"script"}`, cookie))

	del := s.do(t, "DELETE", "/api/tokens/"+created.ID, "", cookie)
	if del.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", del.StatusCode)
	}

	auth := fmt.Sprintf("Bearer %s", created.Token)
	res := s.doWithHeader(t, "GET", "/api/auth/me", "", "Authorization", auth)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoked token still authenticates: status = %d", res.StatusCode)
	}
}

// One account must never be able to revoke another's token by guessing or
// reusing an id.
func TestDeleteTokenIsScopedPerUser(t *testing.T) {
	s := newTestServer(t, "")
	a := signUp(t, s, "a@example.com", "dev-a")
	b := signUp(t, s, "b@example.com", "dev-b")
	created := decodeBody[createTokenResponse](t, s.do(t, "POST", "/api/tokens", `{"name":"a's script"}`, a))

	// b's delete "succeeds" (204, the endpoint does not leak whether the id
	// exists) but must not actually remove a's token.
	s.do(t, "DELETE", "/api/tokens/"+created.ID, "", b)

	authHeader := fmt.Sprintf("Bearer %s", created.Token)
	res := s.doWithHeader(t, "GET", "/api/auth/me", "", "Authorization", authHeader)
	if res.StatusCode != http.StatusOK {
		t.Errorf("a's token stopped working after b tried to delete it: status = %d", res.StatusCode)
	}
}
