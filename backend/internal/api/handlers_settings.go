package api

import (
	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/config"
)

// settingsDTO is config.Settings reshaped for JSON — the instance-wide
// values an admin may read and change from the Settings screen at runtime,
// instead of hand-editing config.yaml and restarting. `server.*` and
// `oidc.*` are deliberately absent: see internal/config's package doc for
// why (transport config a running process cannot rebind itself anyway, and
// a client secret that must never round-trip through a write endpoint).
type settingsDTO struct {
	Registration           string   `json:"registration"`
	DefaultCurrency        string   `json:"defaultCurrency"`
	SessionTTLDays         int      `json:"sessionTtlDays"`
	ChangeLogRetentionDays int      `json:"changeLogRetentionDays"`
	FXEnabled              bool     `json:"fxEnabled"`
	FXRefreshAt            string   `json:"fxRefreshAt"`
	FXProviders            []string `json:"fxProviders"`
}

func toSettingsDTO(s config.Settings) settingsDTO {
	return settingsDTO{
		Registration:           s.App.Registration,
		DefaultCurrency:        s.App.DefaultCurrency,
		SessionTTLDays:         s.App.SessionTTLDays,
		ChangeLogRetentionDays: s.Sync.ChangeLogRetentionDays,
		FXEnabled:              s.FX.On(),
		FXRefreshAt:            s.FX.RefreshAt,
		FXProviders:            s.FX.Providers,
	}
}

// toSettings turns the DTO back into config.Settings. FXEnabled always
// becomes an explicit pointer — never nil — because this request is the one
// place "the operator did not say" cannot happen: the form always submits a
// concrete on/off, unlike a hand-edited config.yaml where an absent
// `fx.enabled` is deliberately allowed to mean "on" (see FX.On()).
func (d settingsDTO) toSettings() config.Settings {
	enabled := d.FXEnabled
	return config.Settings{
		App: config.App{
			Registration:    d.Registration,
			DefaultCurrency: d.DefaultCurrency,
			SessionTTLDays:  d.SessionTTLDays,
		},
		Sync: config.Sync{ChangeLogRetentionDays: d.ChangeLogRetentionDays},
		FX: config.FX{
			Enabled:   &enabled,
			RefreshAt: d.FXRefreshAt,
			Providers: d.FXProviders,
		},
	}
}

// handleGetSettings returns the instance-wide settings an admin may change.
func (s *Server) handleGetSettings(c fiber.Ctx) error {
	return c.JSON(toSettingsDTO(config.SettingsOf(s.config())))
}

// handleUpdateSettings validates and persists a full replacement of the
// editable settings, then swaps the effective config in for every request
// and background loop to read through s.config(). It is a PUT, not a PATCH:
// the client always sends the complete current settings with the one field
// it changed (the same shape handleGetSettings returns), so there is no
// ambiguity about what an absent field would mean.
//
// Validating before UpdateSettings ever touches disk is what keeps a bad
// value a plain 400 — the errorHandler's own rule is that only unexpected
// errors get logged as incidents, and a value the operator got wrong is not
// one of those.
func (s *Server) handleUpdateSettings(c fiber.Ctx) error {
	var in settingsDTO
	if err := decode(c, &in); err != nil {
		return err
	}

	settings := in.toSettings()
	if err := config.ValidateSettings(settings); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	updated, err := config.UpdateSettings(s.configDir, settings)
	if err != nil {
		return err
	}
	s.setConfig(updated)
	// A settings change is exactly the kind of thing fxLoop otherwise would
	// not notice until its next natural wake — up to a day away if `enabled`
	// just flipped on. See fx.go's fxLoop doc.
	s.wakeFX()

	return c.JSON(toSettingsDTO(config.SettingsOf(updated)))
}
