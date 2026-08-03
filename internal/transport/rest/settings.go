package rest

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/domain/setting"
	"github.com/antlko/moneyapp/internal/platform/apperr"
)

func (s *Server) registerSettingRoutes(api fiber.Router) {
	// Groups rather than per-route handler slices: appending to one shared slice
	// of middleware aliases its backing array, and the second append quietly
	// overwrites the first route's handler.
	guard := []fiber.Handler{s.requireSession(), s.requirePasswordChanged()}

	settings := api.Group("/settings", guard...)
	settings.Get("/", s.handleListSettings)
	settings.Patch("/:key", s.requireWritable(), s.handleUpdateSetting)
	settings.Post("/:key/refresh", s.requireWritable(), s.handleRefreshSetting)

	api.Group("/providers", guard...).Get("/", s.handleListProviders)
}

// SettingDTO is one configurable value on the wire.
type SettingDTO struct {
	Key  string `json:"key"`
	Mode string `json:"mode"`
	// ManualValue and LastValue are both shown: "auto suggested 51.08 — you set
	// 51.00" is more useful than either number alone (docs/08-ux.md §8.7).
	ManualValue *string `json:"manual_value"`
	LastValue   *string `json:"last_value"`
	// EffectiveValue is what the application actually uses.
	EffectiveValue *string `json:"effective_value"`
	Overridden     bool    `json:"overridden"`
	ProviderKey    *string `json:"provider_key"`
	LastFetchedAt  *string `json:"last_fetched_at"`
	LastError      *string `json:"last_error"`
	RefreshSeconds int64   `json:"refresh_seconds"`
	Stale          bool    `json:"stale"`
	UpdatedAt      string  `json:"updated_at"`
}

func (s *Server) toSettingDTO(v setting.Setting) SettingDTO {
	out := SettingDTO{
		Key: v.Key, Mode: string(v.Mode),
		ManualValue: v.ManualValue, LastValue: v.LastValue,
		EffectiveValue: v.Effective(), Overridden: v.Overridden(),
		ProviderKey: v.ProviderKey, LastError: v.LastError,
		RefreshSeconds: int64(v.RefreshInterval / time.Second),
		Stale:          v.IsStale(s.deps.Clock.Now()),
		UpdatedAt:      v.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if v.LastFetchedAt != nil {
		at := v.LastFetchedAt.UTC().Format(time.RFC3339)
		out.LastFetchedAt = &at
	}
	return out
}

// ProviderDTO is one configured rate source.
type ProviderDTO struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Endpoint string `json:"endpoint"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
}

// SettingInputDTO is the update payload.
//
// `manual_value: null` and an absent `manual_value` are different requests: the
// first clears an override, the second leaves it alone. That is why the field is
// a double pointer's worth of intent, expressed with an explicit flag below.
type SettingInputDTO struct {
	Mode           *string `json:"mode"`
	ManualValue    *string `json:"manual_value"`
	ClearManual    bool    `json:"clear_manual"`
	ProviderKey    *string `json:"provider_key"`
	RefreshSeconds *int64  `json:"refresh_seconds"`
}

func (s *Server) handleListSettings(c *fiber.Ctx) error {
	u := currentUser(c)
	settings, err := s.deps.Settings.List(c.UserContext(), u.ID)
	if err != nil {
		return err
	}
	out := make([]SettingDTO, 0, len(settings))
	for _, v := range settings {
		out = append(out, s.toSettingDTO(v))
	}
	return c.JSON(out)
}

func (s *Server) handleListProviders(c *fiber.Ctx) error {
	providers, err := s.deps.Settings.ListProviders(c.UserContext())
	if err != nil {
		return err
	}
	out := make([]ProviderDTO, 0, len(providers))
	for _, p := range providers {
		out = append(out, ProviderDTO{
			Key: p.Key, Kind: p.Kind, Endpoint: p.Endpoint,
			Priority: p.Priority, Enabled: p.Enabled,
		})
	}
	return c.JSON(out)
}

func (s *Server) handleUpdateSetting(c *fiber.Ctx) error {
	u := currentUser(c)
	key := c.Params("key")
	var body SettingInputDTO
	if err := bind(c, &body); err != nil {
		return err
	}

	in := setting.Input{
		ManualValue: body.ManualValue,
		ClearManual: body.ClearManual,
		ProviderKey: body.ProviderKey,
	}
	if body.Mode != nil {
		mode := setting.Mode(*body.Mode)
		in.Mode = &mode
	}
	if body.RefreshSeconds != nil {
		if *body.RefreshSeconds < 0 {
			return apperr.Validation("refresh_seconds", "must not be negative")
		}
		interval := time.Duration(*body.RefreshSeconds) * time.Second
		in.RefreshInterval = &interval
	}

	updated, err := s.deps.Settings.Update(c.UserContext(), u.ID, key, in)
	if err != nil {
		return err
	}
	return c.JSON(s.toSettingDTO(updated))
}

func (s *Server) handleRefreshSetting(c *fiber.Ctx) error {
	u := currentUser(c)
	key := c.Params("key")
	updated, err := s.deps.Settings.Refresh(c.UserContext(), u.ID, key)
	if err != nil {
		return err
	}
	return c.JSON(s.toSettingDTO(*updated))
}
