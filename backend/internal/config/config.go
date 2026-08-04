// Package config loads the instance's config.yaml.
//
// Unlike upmonitor, moneyfly's config.yaml holds **no user data** — accounts,
// categories and transactions all live in SQLite because they are synced. What
// remains here is instance infrastructure the operator hand-edits: listen
// address, public URL, OIDC providers, FX provider chain, retention. That is why
// this package is load-only: there is no Save, no Clone and no copy-on-write
// path, and the API never exposes the raw file (it contains OIDC secrets).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Defaults live here so API-layer fallbacks cannot drift from what a fresh
// config gets.
const (
	DefaultConfigDir              = "./config"
	DefaultAddr                   = ":5007"
	DefaultCurrency               = "EUR"
	DefaultSessionTTLDays         = 365
	DefaultChangeLogRetentionDays = 90
	DefaultFXRefreshAt            = "04:00"
)

// FXProviders are the provider ids `fx.providers` accepts, in the order a fresh
// config gets them: the primary first, the fallback second.
//
// Validation checks membership rather than letting an unknown id be skipped
// silently — a typo there is otherwise invisible until someone notices that
// rates stopped updating a month ago.
var FXProviders = []string{"open-er-api", "fawazahmed0"}

// Config is the whole of config.yaml.
type Config struct {
	Server Server         `yaml:"server"`
	App    App            `yaml:"app"`
	Sync   Sync           `yaml:"sync"`
	FX     FX             `yaml:"fx"`
	OIDC   []OIDCProvider `yaml:"oidc"`
}

// Server holds HTTP transport settings.
type Server struct {
	Addr string `yaml:"addr"`
	// BaseURL is the externally reachable origin (e.g. https://money.example.com).
	// Required for OIDC, which must hand the provider an absolute redirect URI.
	BaseURL string `yaml:"base_url"`
}

// App holds instance-wide product settings.
type App struct {
	// Registration is "open" (anyone may create an account) or "closed" (only
	// the first account, created on the setup screen, may exist).
	Registration    string `yaml:"registration"`
	DefaultCurrency string `yaml:"default_currency"`
	SessionTTLDays  int    `yaml:"session_ttl_days"`
}

// Sync holds settings for the device sync engine.
type Sync struct {
	// ChangeLogRetentionDays bounds the change_log table. Devices that have been
	// offline longer than this re-bootstrap from /api/sync/snapshot rather than
	// replaying deltas — see docs/SYNC.md.
	ChangeLogRetentionDays int `yaml:"change_log_retention_days"`
}

// FX configures daily exchange-rate refresh.
type FX struct {
	Enabled   bool     `yaml:"enabled"`
	RefreshAt string   `yaml:"refresh_at"`
	Providers []string `yaml:"providers"`
}

// RefreshHourMinute parses RefreshAt into a wall-clock time of day.
func (f FX) RefreshHourMinute() (hour, minute int, err error) {
	if _, err := fmt.Sscanf(f.RefreshAt, "%d:%d", &hour, &minute); err != nil {
		return 0, 0, fmt.Errorf("fx.refresh_at: want HH:MM, got %q", f.RefreshAt)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("fx.refresh_at: %q is not a time of day", f.RefreshAt)
	}
	return hour, minute, nil
}

func (f FX) validate() error {
	if _, _, err := f.RefreshHourMinute(); err != nil {
		return err
	}
	// An unknown id is a startup failure rather than a skipped provider: the
	// symptom of skipping is rates that quietly stop moving.
	for _, id := range f.Providers {
		if !slices.Contains(FXProviders, id) {
			return fmt.Errorf("fx.providers: unknown provider %q, want one of %s",
				id, strings.Join(FXProviders, ", "))
		}
	}
	if f.Enabled && len(f.Providers) == 0 {
		return fmt.Errorf("fx.enabled is true but fx.providers is empty")
	}
	return nil
}

// OIDCProvider is one configured identity provider. Any standards-compliant
// issuer works (Google, Authentik, Keycloak, ...); nothing here is Google-specific.
type OIDCProvider struct {
	ID           string   `yaml:"id"`
	Name         string   `yaml:"name"`
	Issuer       string   `yaml:"issuer"`
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	Scopes       []string `yaml:"scopes"`
}

// Registration modes.
const (
	RegistrationOpen   = "open"
	RegistrationClosed = "closed"
)

// EnsureDir creates the config directory if it does not exist.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create config dir %q: %w", dir, err)
	}
	return nil
}

// Path returns the config file path for a config directory.
func Path(dir string) string { return filepath.Join(dir, "config.yaml") }

// DBPath returns the SQLite database path for a config directory.
func DBPath(dir string) string { return filepath.Join(dir, "moneyfly.db") }

// Load reads config.yaml from dir. A missing file is not an error: the caller
// gets a fully defaulted config, so a bare `docker run` with an empty volume
// starts. Environment variables are applied last and always win.
func Load(dir string) (*Config, error) {
	cfg := &Config{}
	data, err := os.ReadFile(Path(dir))
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", Path(dir), err)
		}
	case os.IsNotExist(err):
		// Defaults only.
	default:
		return nil, fmt.Errorf("read %s: %w", Path(dir), err)
	}

	cfg.normalize()
	cfg.applyEnv()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// normalize fills zero values with defaults. It only ever fills zeros, so an
// existing config keeps every value the operator set.
func (c *Config) normalize() {
	if c.Server.Addr == "" {
		c.Server.Addr = DefaultAddr
	}
	if c.App.Registration == "" {
		c.App.Registration = RegistrationOpen
	}
	if c.App.DefaultCurrency == "" {
		c.App.DefaultCurrency = DefaultCurrency
	}
	if c.App.SessionTTLDays == 0 {
		c.App.SessionTTLDays = DefaultSessionTTLDays
	}
	if c.Sync.ChangeLogRetentionDays == 0 {
		c.Sync.ChangeLogRetentionDays = DefaultChangeLogRetentionDays
	}
	if c.FX.RefreshAt == "" {
		c.FX.RefreshAt = DefaultFXRefreshAt
	}
	if len(c.FX.Providers) == 0 {
		c.FX.Providers = append([]string(nil), FXProviders...)
	}
	for i := range c.OIDC {
		p := &c.OIDC[i]
		if p.Name == "" {
			p.Name = p.ID
		}
		if len(p.Scopes) == 0 {
			p.Scopes = []string{"openid", "email", "profile"}
		}
	}
}

// applyEnv overlays environment variables. Secrets in particular are better kept
// out of the file in container deployments, so every OIDC client secret can be
// supplied as MONEYFLY_OIDC_<ID>_CLIENT_SECRET (id upper-cased, '-' → '_').
func (c *Config) applyEnv() {
	if v := os.Getenv("MONEYFLY_ADDR"); v != "" {
		c.Server.Addr = v
	}
	if v := os.Getenv("MONEYFLY_BASE_URL"); v != "" {
		c.Server.BaseURL = v
	}
	if v := os.Getenv("MONEYFLY_REGISTRATION"); v != "" {
		c.App.Registration = v
	}
	for i := range c.OIDC {
		p := &c.OIDC[i]
		key := "MONEYFLY_OIDC_" + strings.ToUpper(strings.ReplaceAll(p.ID, "-", "_"))
		if v := os.Getenv(key + "_CLIENT_ID"); v != "" {
			p.ClientID = v
		}
		if v := os.Getenv(key + "_CLIENT_SECRET"); v != "" {
			p.ClientSecret = v
		}
	}
}

// Validate rejects a config that would misbehave at runtime. A failure here
// stops startup — better than a provider that silently never works.
func (c *Config) Validate() error {
	switch c.App.Registration {
	case RegistrationOpen, RegistrationClosed:
	default:
		return fmt.Errorf("app.registration: want %q or %q, got %q",
			RegistrationOpen, RegistrationClosed, c.App.Registration)
	}
	if len(c.App.DefaultCurrency) != 3 {
		return fmt.Errorf("app.default_currency: want a 3-letter code, got %q", c.App.DefaultCurrency)
	}
	if c.Sync.ChangeLogRetentionDays < 1 {
		return fmt.Errorf("sync.change_log_retention_days: want >= 1, got %d", c.Sync.ChangeLogRetentionDays)
	}
	if err := c.FX.validate(); err != nil {
		return err
	}

	seen := make(map[string]bool, len(c.OIDC))
	for _, p := range c.OIDC {
		switch {
		case p.ID == "":
			return fmt.Errorf("oidc: a provider is missing id")
		case seen[p.ID]:
			return fmt.Errorf("oidc: duplicate provider id %q", p.ID)
		case p.Issuer == "":
			return fmt.Errorf("oidc[%s]: issuer is required", p.ID)
		case p.ClientID == "":
			return fmt.Errorf("oidc[%s]: client_id is required", p.ID)
		}
		seen[p.ID] = true
	}
	if len(c.OIDC) > 0 && c.Server.BaseURL == "" {
		return fmt.Errorf("server.base_url is required when oidc providers are configured")
	}
	return nil
}

// Provider returns the OIDC provider with the given id.
func (c *Config) Provider(id string) *OIDCProvider {
	for i := range c.OIDC {
		if c.OIDC[i].ID == id {
			return &c.OIDC[i]
		}
	}
	return nil
}
