package api

import (
	"github.com/gofiber/fiber/v3"
)

// Version is the build version, overridden at link time:
//
//	go build -ldflags="-X moneyfly/internal/api.Version=v1.2.3"
var Version = "dev"

// handleHealth reports liveness plus the handful of instance facts the SPA needs
// before anyone is signed in: which sign-in methods to offer, whether to show
// the "create account" form, and what currency a new account defaults to.
func (s *Server) handleHealth(c fiber.Ctx) error {
	cfg := s.config()

	// A closed instance with no accounts yet still shows the create form — that
	// is how the operator claims it. See handleRegister.
	allowed, err := s.registrationAllowed()
	if err != nil {
		return err
	}
	// `claimed` distinguishes the two reasons registration might be allowed: a
	// brand-new instance (show "claim this instance") from an open one that
	// already has accounts (show "sign in", with create as the alternative).
	users, err := s.conn().CountUsers()
	if err != nil {
		return err
	}

	providers := make([]fiber.Map, 0)
	for _, p := range s.providers().List() {
		// Only id and name — never the client id or secret.
		providers = append(providers, fiber.Map{"id": p.ID, "name": p.Name})
	}

	return c.JSON(fiber.Map{
		"status":              "ok",
		"version":             Version,
		"registrationAllowed": allowed,
		"claimed":             users > 0,
		"defaultCurrency":     cfg.App.DefaultCurrency,
		"oidcProviders":       providers,
		// Whether to offer the passkey button at all. Derived, not configured:
		// passkeys need a public URL to bind credentials to (see
		// auth.NewWebAuthn), so this is false until server.base_url is set.
		"webauthnEnabled": s.webAuthn() != nil,
	})
}
