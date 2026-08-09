package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"moneyfly/internal/auth"
	"moneyfly/internal/db"
)

// webAuthnSessionTTL bounds how long a started ceremony stays answerable.
//
// Shorter than OIDC's ten minutes because there is no third party to wait on —
// just a local prompt for a fingerprint or a PIN. Five minutes is still
// generous for someone who has to go and find a security key.
const webAuthnSessionTTL = 5 * time.Minute

// webAuthnUser adapts an account and its stored passkeys to the interface
// go-webauthn wants.
//
// It lives in this package rather than internal/auth for the same reason
// handlers_oidc.go imports oauth2 directly: internal/auth holds protocol
// mechanics and stays independent of db's schema, and this is where the two
// meet.
type webAuthnUser struct {
	user  *db.User
	creds []webauthn.Credential
}

func newWebAuthnUser(u *db.User, rows []db.WebAuthnCredential) (*webAuthnUser, error) {
	creds := make([]webauthn.Credential, 0, len(rows))
	for _, r := range rows {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(r.Data), &c); err != nil {
			return nil, fmt.Errorf("decode stored passkey %s: %w", r.ID, err)
		}
		creds = append(creds, c)
	}
	return &webAuthnUser{user: u, creds: creds}, nil
}

// WebAuthnID is the account's own id as raw bytes.
//
// Using the existing UUID, rather than minting a separate WebAuthn-specific
// handle, is what lets a usernameless sign-in resolve with a plain UserByID:
// the handle the authenticator hands back *is* the account id, so there is no
// second mapping table to keep in step.
func (w *webAuthnUser) WebAuthnID() []byte {
	id, err := uuid.Parse(w.user.ID)
	if err != nil {
		// Unreachable: every user id is minted by db.NewID as a UUIDv7. Falling
		// back to the raw bytes keeps this total rather than panicking in a
		// request path.
		return []byte(w.user.ID)
	}
	return id[:]
}

func (w *webAuthnUser) WebAuthnName() string                       { return w.user.Email }
func (w *webAuthnUser) WebAuthnDisplayName() string                { return w.user.DisplayName }
func (w *webAuthnUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }
func (w *webAuthnUser) WebAuthnIcon() string                       { return "" }

// resolveDiscoverableUser implements go-webauthn's DiscoverableUserHandler.
//
// The user handle is exactly what WebAuthnID returns, so this is a direct id
// lookup — never an email, never a claim from anywhere else. That is what keeps
// passkey sign-in free of the account-resolution question OIDC has to answer
// carefully (docs/ARCHITECTURE.md §4): the credential itself names the account,
// and it can only do so because this instance issued it.
func (s *Server) resolveDiscoverableUser(_, userHandle []byte) (webauthn.User, error) {
	id, err := uuid.FromBytes(userHandle)
	if err != nil {
		return nil, fmt.Errorf("malformed passkey user handle: %w", err)
	}
	u, err := s.conn().UserByID(id.String())
	if err != nil {
		return nil, err
	}
	rows, err := s.conn().WebAuthnCredentialsForUser(u.ID)
	if err != nil {
		return nil, err
	}
	return newWebAuthnUser(u, rows)
}

// requireWebAuthn is the ceremony endpoints' precondition.
//
// Only the four ceremony routes call it. Listing and deleting credentials
// deliberately do not: those are ordinary database operations, and an
// already-registered passkey must stay visible and removable even if
// server.base_url is later unset — exactly as an OIDC identity stays linked and
// unlinkable after its provider is dropped from the config.
func (s *Server) requireWebAuthn() (*webauthn.WebAuthn, error) {
	wa := s.webAuthn()
	if wa == nil {
		return nil, fiber.NewError(fiber.StatusServiceUnavailable,
			"passkeys are not available on this instance — it has no configured public URL")
	}
	return wa, nil
}

// saveWebAuthnSession stores the challenge state a ceremony will be verified
// against, and returns the id the client threads back to the verify call.
func (s *Server) saveWebAuthnSession(userID, purpose string, session *webauthn.SessionData) (string, error) {
	id, err := auth.NewURLToken()
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	if err := s.conn().SaveWebAuthnSession(db.WebAuthnSession{
		ID:      id,
		UserID:  userID,
		Purpose: purpose,
		Data:    string(data),
	}, webAuthnSessionTTL); err != nil {
		return "", err
	}
	return id, nil
}

// takeWebAuthnSession consumes a pending ceremony and decodes it.
//
// wantUserID is empty for a sign-in, whose session is not tied to an account
// yet; for a registration it must match the caller, so one account cannot
// finish a ceremony another one started.
func (s *Server) takeWebAuthnSession(id, wantPurpose, wantUserID string) (*webauthn.SessionData, error) {
	row, err := s.conn().TakeWebAuthnSession(id)
	if err != nil {
		return nil, err
	}
	if row.Purpose != wantPurpose || row.UserID != wantUserID {
		return nil, db.ErrNotFound
	}
	var data webauthn.SessionData
	if err := json.Unmarshal([]byte(row.Data), &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// --- Registration: adding a passkey to the account already signed in ------------

func (s *Server) handleWebAuthnRegisterOptions(c fiber.Ctx) error {
	wa, err := s.requireWebAuthn()
	if err != nil {
		return err
	}
	user := userLocal(c)

	rows, err := s.conn().WebAuthnCredentialsForUser(user.ID)
	if err != nil {
		return err
	}
	waUser, err := newWebAuthnUser(user, rows)
	if err != nil {
		return err
	}

	// Excluding what is already registered is what makes the authenticator say
	// "you already have a passkey here" instead of silently minting a second
	// one for the same account on the same device.
	creation, session, err := wa.BeginRegistration(waUser,
		webauthn.WithExclusions(webauthn.Credentials(waUser.creds).CredentialDescriptors()))
	if err != nil {
		return err
	}

	sessionID, err := s.saveWebAuthnSession(user.ID, db.WebAuthnPurposeRegistration, session)
	if err != nil {
		return err
	}
	// creation.Response, not creation: the browser library takes the bare
	// options object, and wrapping it in the spec's outer `publicKey` envelope
	// here would hand it a shape it does not unwrap.
	return c.JSON(fiber.Map{"sessionId": sessionID, "publicKey": creation.Response})
}

func (s *Server) handleWebAuthnRegisterVerify(c fiber.Ctx) error {
	wa, err := s.requireWebAuthn()
	if err != nil {
		return err
	}
	user := userLocal(c)

	var in struct {
		SessionID  string          `json:"sessionId"`
		Name       string          `json:"name"`
		Credential json.RawMessage `json:"credential"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}

	session, err := s.takeWebAuthnSession(in.SessionID, db.WebAuthnPurposeRegistration, user.ID)
	if errors.Is(err, db.ErrNotFound) {
		return fiber.NewError(fiber.StatusBadRequest, "this passkey request has expired — try again")
	}
	if err != nil {
		return err
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(in.Credential)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "the browser's passkey response could not be read")
	}

	rows, err := s.conn().WebAuthnCredentialsForUser(user.ID)
	if err != nil {
		return err
	}
	waUser, err := newWebAuthnUser(user, rows)
	if err != nil {
		return err
	}

	cred, err := wa.CreateCredential(waUser, *session, parsed)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "this passkey could not be verified")
	}

	blob, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Passkey"
	}

	row, err := s.conn().CreateWebAuthnCredential(db.NewID(), user.ID,
		base64.RawURLEncoding.EncodeToString(cred.ID), name, string(blob))
	if err != nil {
		if errors.Is(err, db.ErrCredentialTaken) {
			return fiber.NewError(fiber.StatusConflict, "this passkey is already registered")
		}
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(toWebAuthnCredentialDTO(*row))
}

// --- Sign-in: usernameless, resolved from the credential itself -----------------

func (s *Server) handleWebAuthnLoginOptions(c fiber.Ctx) error {
	wa, err := s.requireWebAuthn()
	if err != nil {
		return err
	}

	// Discoverable: no account is named, and no allowCredentials list is sent,
	// so the browser offers whichever passkeys it holds for this origin. That
	// is also why this endpoint leaks nothing — it answers identically whether
	// or not any account exists.
	assertion, session, err := wa.BeginDiscoverableLogin()
	if err != nil {
		return err
	}
	sessionID, err := s.saveWebAuthnSession("", db.WebAuthnPurposeLogin, session)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"sessionId": sessionID, "publicKey": assertion.Response})
}

func (s *Server) handleWebAuthnLoginVerify(c fiber.Ctx) error {
	wa, err := s.requireWebAuthn()
	if err != nil {
		return err
	}

	var in struct {
		SessionID  string          `json:"sessionId"`
		Credential json.RawMessage `json:"credential"`
		DeviceID   string          `json:"deviceId"`
		Platform   string          `json:"platform"`
	}
	if err := decode(c, &in); err != nil {
		return err
	}

	// One message for every failure past this point, the same reasoning
	// handleLogin's shared bad-credentials string follows: an expired
	// challenge, an unknown credential and a bad signature must not be
	// distinguishable from outside.
	const failed = "passkey sign-in failed"

	session, err := s.takeWebAuthnSession(in.SessionID, db.WebAuthnPurposeLogin, "")
	if errors.Is(err, db.ErrNotFound) {
		return fiber.NewError(fiber.StatusUnauthorized, failed)
	}
	if err != nil {
		return err
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(in.Credential)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, failed)
	}

	resolved, cred, err := wa.ValidatePasskeyLogin(s.resolveDiscoverableUser, *session, parsed)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, failed)
	}
	waUser, ok := resolved.(*webAuthnUser)
	if !ok {
		return fmt.Errorf("webauthn: unexpected user type %T from passkey login", resolved)
	}

	// Store the credential back with its advanced signature counter before
	// issuing the session — see db.TouchWebAuthnCredential for why the counter
	// matters rather than just the timestamp.
	blob, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	if err := s.conn().TouchWebAuthnCredential(
		base64.RawURLEncoding.EncodeToString(cred.ID), string(blob)); err != nil {
		return err
	}

	return s.startSession(c, waUser.user, in.DeviceID, in.Platform)
}

// --- Management -----------------------------------------------------------------

func (s *Server) handleListWebAuthnCredentials(c fiber.Ctx) error {
	rows, err := s.conn().WebAuthnCredentialsForUser(userLocal(c).ID)
	if err != nil {
		return err
	}
	out := make([]WebAuthnCredentialDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, toWebAuthnCredentialDTO(r))
	}
	return c.JSON(out)
}

func (s *Server) handleDeleteWebAuthnCredential(c fiber.Ctx) error {
	err := s.conn().DeleteWebAuthnCredential(userLocal(c).ID, c.Params("id"))
	switch {
	case errors.Is(err, db.ErrLastSignInMethod):
		return fiber.NewError(fiber.StatusConflict,
			"set a password or add another sign-in method before removing your only passkey")
	case errors.Is(err, db.ErrNotFound):
		return fiber.NewError(fiber.StatusNotFound, "passkey not found")
	case err != nil:
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
