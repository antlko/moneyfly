package api

import (
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/oauth2"

	"moneyfly/internal/auth"
	"moneyfly/internal/db"
)

// oidcStateTTL bounds how long an in-flight authorisation stays valid. Long
// enough to type a password and approve an MFA prompt, short enough that an
// abandoned one is gone before it matters.
const oidcStateTTL = 10 * time.Minute

// handleOIDCStart redirects the browser to the identity provider.
//
// Query parameters:
//
//	link=1     link this provider to the account already signed in here
//	redirect=  where to land in the SPA afterwards (same-origin path only)
func (s *Server) handleOIDCStart(c fiber.Ctx) error {
	providerID := c.Params("provider")
	registry := s.providers()

	state, err := auth.NewURLToken()
	if err != nil {
		return err
	}
	nonce, err := auth.NewURLToken()
	if err != nil {
		return err
	}
	verifier := oauth2.GenerateVerifier()

	pending := db.OIDCState{
		State:        state,
		Provider:     providerID,
		Nonce:        nonce,
		CodeVerifier: verifier,
		RedirectTo:   safeRedirect(c.Query("redirect")),
	}
	if c.Query("link") == "1" {
		user := s.currentUser(c)
		if user == nil {
			return fiber.NewError(fiber.StatusUnauthorized, "sign in before linking a provider")
		}
		pending.LinkUserID = user.ID
	}

	authURL, err := registry.AuthCodeURL(c.Context(), providerID, state, nonce, verifier)
	if errors.Is(err, auth.ErrUnknownProvider) {
		return fiber.NewError(fiber.StatusNotFound, "unknown identity provider")
	}
	if err != nil {
		// Discovery is lazy, so an unreachable provider surfaces here rather
		// than at startup. Say so plainly instead of rendering a 500.
		slog.ErrorContext(c.Context(), "oidc: discovery failed", "provider", providerID, "error", err)
		return fiber.NewError(fiber.StatusBadGateway, "identity provider is unreachable")
	}
	if err := s.conn().SaveOIDCState(pending, oidcStateTTL); err != nil {
		return err
	}
	return c.Redirect().Status(fiber.StatusFound).To(authURL)
}

// handleOIDCCallback completes the flow and signs the browser in.
//
// Account resolution, in order:
//
//  1. the (provider, subject) pair is already linked → sign that account in;
//  2. the flow was started with link=1 → attach the provider to that account;
//  3. registration is allowed → create a new account for the claimed email;
//  4. otherwise → refuse.
//
// Note what step 3 deliberately does **not** do: if the email already belongs to
// a password account, it does not silently adopt it. Auto-linking on a claimed
// email is an account-takeover route whenever the provider does not verify
// emails, so the user is told to sign in normally and link from settings.
func (s *Server) handleOIDCCallback(c fiber.Ctx) error {
	if e := c.Query("error"); e != "" {
		return s.oidcFailure(c, e)
	}

	pending, err := s.conn().TakeOIDCState(c.Query("state"))
	if errors.Is(err, db.ErrNotFound) {
		return s.oidcFailure(c, "this sign-in link has expired — please try again")
	}
	if err != nil {
		return err
	}
	if pending.Provider != c.Params("provider") {
		return s.oidcFailure(c, "sign-in state did not match")
	}

	claims, err := s.providers().Exchange(c.Context(), pending.Provider,
		c.Query("code"), pending.Nonce, pending.CodeVerifier)
	if err != nil {
		slog.ErrorContext(c.Context(), "oidc: exchange failed",
			"provider", pending.Provider, "error", err)
		return s.oidcFailure(c, "the identity provider rejected the sign-in")
	}

	database := s.conn()

	// 1. Already linked.
	identity, err := database.IdentityBySubject(pending.Provider, claims.Subject)
	if err == nil {
		user, err := database.UserByID(identity.UserID)
		if err != nil {
			return err
		}
		return s.finishOIDC(c, user, pending.RedirectTo)
	}
	if !errors.Is(err, db.ErrNotFound) {
		return err
	}

	// 2. Explicit link to the account already signed in.
	if pending.LinkUserID != "" {
		user, err := database.UserByID(pending.LinkUserID)
		if err != nil {
			return err
		}
		if _, err := database.CreateIdentity(user.ID, pending.Provider,
			claims.Subject, claims.Email); err != nil {
			if errors.Is(err, db.ErrIdentityTaken) {
				return s.oidcFailure(c, "that provider account is already linked elsewhere")
			}
			return err
		}
		return s.finishOIDC(c, user, pending.RedirectTo)
	}

	// 3. First sign-in through this provider.
	if claims.Email == "" {
		return s.oidcFailure(c,
			"the identity provider did not share an email address")
	}
	if _, err := database.UserByEmail(claims.Email); err == nil {
		return s.oidcFailure(c,
			"an account with that email already exists — sign in with your password, "+
				"then link this provider from settings")
	} else if !errors.Is(err, db.ErrNotFound) {
		return err
	}

	allowed, err := s.registrationAllowed()
	if err != nil {
		return err
	}
	if !allowed {
		return s.oidcFailure(c, "registration is closed on this instance")
	}

	name := claims.Name
	if name == "" {
		name, _, _ = strings.Cut(claims.Email, "@")
	}
	user, err := database.CreateUser(claims.Email, "", name, s.config().App.DefaultCurrency)
	if err != nil {
		return err
	}
	if _, err := database.CreateIdentity(user.ID, pending.Provider,
		claims.Subject, claims.Email); err != nil {
		return err
	}
	return s.finishOIDC(c, user, pending.RedirectTo)
}

// finishOIDC sets the session cookie and bounces back into the SPA. Unlike the
// password endpoints this answers with a redirect, because the browser got here
// by navigating, not by fetch.
func (s *Server) finishOIDC(c fiber.Ctx, user *db.User, redirectTo string) error {
	token, err := auth.NewToken()
	if err != nil {
		return err
	}
	ttl := time.Duration(s.config().App.SessionTTLDays) * 24 * time.Hour

	// The device id lives in IndexedDB, which the server cannot read mid-redirect;
	// the SPA calls /api/sync once it loads and the device registers then.
	if err := s.conn().CreateSession(auth.HashToken(token), user.ID, "",
		string(c.Request().Header.UserAgent()), ttl); err != nil {
		return err
	}
	s.setSessionCookie(c, token, ttl)
	return c.Redirect().Status(fiber.StatusFound).To(redirectTo)
}

// oidcFailure sends the browser back to the sign-in screen with a message,
// rather than rendering a JSON error into a top-level navigation.
func (s *Server) oidcFailure(c fiber.Ctx, reason string) error {
	return c.Redirect().Status(fiber.StatusFound).
		To("/signin?error=" + url.QueryEscape(reason))
}

// safeRedirect keeps the post-sign-in destination on this origin. Without it the
// start endpoint would be an open redirect: anyone could hand out a moneyfly
// link that lands on their own page.
func safeRedirect(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return "/"
	}
	return raw
}
