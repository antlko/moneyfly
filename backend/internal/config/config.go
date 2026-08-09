// Package config loads the instance's config.yaml, and writes back the one
// slice of it — `app`, `sync`, `fx` — an admin may change from the Settings
// screen at runtime.
//
// Unlike upmonitor, moneyfly's config.yaml holds **no user data** — accounts,
// categories and transactions all live in SQLite because they are synced. What
// remains here is instance infrastructure: listen address, public URL, OIDC
// providers, FX provider chain, retention. `server.*` and `oidc.*` stay
// hand-edited-file-or-env only, on purpose — `server.*` is transport
// configuration a running process cannot rebind itself anyway, and `oidc.*`
// can carry a client secret, which must never round-trip through a write
// endpoint. UpdateSettings enforces this by re-reading `server`/`oidc` from
// disk and leaving them untouched rather than writing back whatever the
// in-memory Config happens to hold — which, for OIDC, may already contain a
// secret pulled in from the environment by applyEnv, and that must never be
// the thing written to a file.
package config

import (
	"fmt"
	"log/slog"
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
	// DefaultFXEnabled is on. Conversion is not an opt-in extra: a record in a
	// currency that cannot be converted is left out of every total on the
	// dashboard, so an instance that fetches no rates is one that quietly
	// under-reports what was spent.
	DefaultFXEnabled = true
	// MaxSessionTTLDays bounds session_ttl_days. Ten years is far past any
	// sensible setting and well short of a typo'd 36500 that would make a
	// session effectively permanent — an upper bound is worth as much as the
	// lower one on a field that governs how long a stolen cookie stays good.
	MaxSessionTTLDays = 3650
)

// FXProviders are the provider ids `fx.providers` accepts, in the order a fresh
// config gets them: the primary first, the fallback second.
//
// Validation checks membership rather than letting an unknown id be skipped
// silently — a typo there is otherwise invisible until someone notices that
// rates stopped updating a month ago.
var FXProviders = []string{"open-er-api", "fawazahmed0"}

// Config is the whole of config.yaml, plus the transport settings that come
// from the environment rather than the file.
type Config struct {
	// LegacyServer is the `server:` block as pre-migration files still carry
	// it. Nil — and so omitted from anything written — on every instance
	// created after transport moved to the environment, which is what stops a
	// fresh config.yaml growing the section back. See adoptLegacyServerBlock.
	LegacyServer *legacyServer  `yaml:"server,omitempty"`
	App          App            `yaml:"app"`
	Sync         Sync           `yaml:"sync"`
	FX           FX             `yaml:"fx"`
	OIDC         []OIDCProvider `yaml:"oidc"`

	// Server is `yaml:"-"` on purpose: transport is environment-only. See the
	// Server doc below.
	Server Server `yaml:"-"`
}

// Server holds HTTP transport settings. **Environment-only** —
// MONEYFLY_ADDR and MONEYFLY_BASE_URL — and deliberately never written to
// config.yaml.
//
// Every other field on Config has exactly one owner: the file, edited either by
// hand or through the Settings screen. These two are the exception in the other
// direction, and having them in both places was the problem. A running process
// cannot rebind its own listen address, so `addr` in a file is a value that
// looks editable and silently is not until the next restart; `base_url` is
// deployment topology that belongs with the reverse proxy that terminates TLS,
// not with the app's product settings. Splitting them out means no field
// anywhere is settable from two places at once.
type Server struct {
	Addr string
	// BaseURL is the externally reachable origin (e.g. https://money.example.com).
	// Required for OIDC, which must hand the provider an absolute redirect URI.
	BaseURL string
}

// legacyServer is the `server:` section as older config.yaml files still carry
// it, kept only so those instances keep working.
//
// Every install created before transport moved to the environment has this
// block, and an operator running OIDC behind a proxy has a real `base_url` in
// it. Dropping it would move their listener back to :5007 and fail their OIDC
// startup on the base_url check — and dropping it *on the next save*, which is
// what `yaml:"-"` alone would do, is worse still: the instance keeps working
// until someone changes an unrelated setting, and breaks on the restart after
// that, with no visible connection between the two.
//
// So it is honoured, warned about, and **written back unchanged** until the
// matching environment variable supplies the value. Setting the variable is
// what releases it: adoptLegacyServerBlock clears the field once the
// environment owns it, and the next save is what finally removes the section.
type legacyServer struct {
	Addr    string `yaml:"addr,omitempty"`
	BaseURL string `yaml:"base_url,omitempty"`
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
//
// Enabled is a *pointer* so that "absent" and "false" are different answers. A
// plain bool cannot tell them apart, and its zero value is `false` — which meant
// that an instance with no config.yaml at all (the documented, supported way to
// run this: `docker run` with an empty volume) silently never fetched a single
// rate. Every foreign-currency record then sat outside the totals forever,
// captioned "no exchange rate yet", with nothing anywhere to say why.
type FX struct {
	Enabled   *bool    `yaml:"enabled"`
	RefreshAt string   `yaml:"refresh_at"`
	Providers []string `yaml:"providers"`
}

// On reports whether the refresh loop should run. Absent means on.
func (f FX) On() bool { return f.Enabled == nil || *f.Enabled }

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
	if f.On() && len(f.Providers) == 0 {
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

// Environment variables this package reads. Named rather than inlined so the
// deprecation warnings and the docs can point at the same strings.
const (
	envAddr         = "MONEYFLY_ADDR"
	envBaseURL      = "MONEYFLY_BASE_URL"
	envRegistration = "MONEYFLY_REGISTRATION"
	EnvAdminEmail   = "MONEYFLY_ADMIN_EMAIL"
)

// AdminEmail is the account to promote to administrator on startup, or "".
//
// Admin is otherwise granted to whoever registers first and can only be handed
// out by an existing admin — which is a dead end on a real instance: if the
// first account was a throwaway, or belongs to someone who has left, nobody
// left can reach Settings → Instance, and every admin action answers 403 with
// no way to fix it from inside the app. The image is distroless, so there is no
// shell to run a query in either.
//
// Read from the environment rather than config.yaml because it is a recovery
// lever an operator pulls once, not a setting: set it, restart, unset it.
func AdminEmail() string { return strings.TrimSpace(os.Getenv(EnvAdminEmail)) }

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

// Load reads config.yaml from dir. A missing file is not an error: a bare
// `docker run` against an empty volume gets a fully defaulted config — and
// that full set of defaults is also written to config.yaml on the spot, so
// the file an operator opens next always shows every field there is to
// adjust, rather than the operator having to already know a field exists
// before they can set it.
//
// Ordering matters and is: parse → seed (first boot only) → normalize → save →
// applyEnv. The save happens before applyEnv so a transport variable is never
// what ends up baked into the file, and the seed happens before the save
// precisely so it *is* — see seedFromEnv.
func Load(dir string) (*Config, error) {
	cfg := &Config{}
	data, err := os.ReadFile(Path(dir))
	existed := true
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", Path(dir), err)
		}
	case os.IsNotExist(err):
		existed = false
	default:
		return nil, fmt.Errorf("read %s: %w", Path(dir), err)
	}

	if !existed {
		cfg.seedFromEnv()
	} else {
		cfg.adoptLegacyServerBlock()
	}

	cfg.normalize()
	if !existed {
		if err := save(dir, cfg); err != nil {
			return nil, err
		}
	}
	cfg.applyEnv()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// seedFromEnv supplies the initial value of a *settings* field from the
// environment, on first boot only.
//
// MONEYFLY_REGISTRATION is the one field where this is needed. Registration is
// a Settings-screen value, so it must have a single owner — the file — or
// saving any unrelated setting silently bakes a transient environment value in,
// and changing the dropdown reverts under the operator on the next read. But it
// is also the only way to bring up a publicly reachable instance that is not
// open to whoever finds it first: `closed` still permits the operator's own
// first account (see registrationAllowed) and blocks everyone after.
//
// Seeding resolves both. The variable decides what a brand-new instance starts
// as, is written into config.yaml like any other default, and is then never
// consulted again — from that moment the file, and the Settings screen over it,
// is the only owner.
func (c *Config) seedFromEnv() {
	if v := os.Getenv(envRegistration); v != "" {
		c.App.Registration = v
	}
}

// adoptLegacyServerBlock keeps pre-existing installs working after transport
// moved to environment-only. See legacyServer.
//
// Called after the file has been unmarshalled (which is what populates
// LegacyServer) and before applyServerEnv, so the environment still wins.
func (c *Config) adoptLegacyServerBlock() {
	if c.LegacyServer == nil {
		return
	}
	// Whatever the environment supplies, it owns — drop that half of the block
	// so the next save stops writing it. When the environment supplies both,
	// the section disappears entirely and the migration is complete.
	if os.Getenv(envAddr) != "" {
		c.LegacyServer.Addr = ""
	}
	if os.Getenv(envBaseURL) != "" {
		c.LegacyServer.BaseURL = ""
	}

	if c.LegacyServer.Addr != "" {
		c.Server.Addr = c.LegacyServer.Addr
		slog.Warn("config: server.addr in config.yaml is deprecated; set the environment "+
			"variable instead, and this section will be removed on the next save",
			"value", c.LegacyServer.Addr, "env", envAddr)
	}
	if c.LegacyServer.BaseURL != "" {
		c.Server.BaseURL = c.LegacyServer.BaseURL
		slog.Warn("config: server.base_url in config.yaml is deprecated; set the environment "+
			"variable instead, and this section will be removed on the next save",
			"value", c.LegacyServer.BaseURL, "env", envBaseURL)
	}
	if c.LegacyServer.Addr == "" && c.LegacyServer.BaseURL == "" {
		c.LegacyServer = nil
	}
}

// save marshals cfg as YAML and writes it to dir's config.yaml, whole. 0o600
// because a config.yaml an operator goes on to hand-edit is exactly where an
// OIDC client secret is documented to belong (docs/CONFIGURATION.md) — this
// package's own write path never puts one there, but the file permissions
// have to assume one might arrive by another route.
func save(dir string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	if err := os.WriteFile(Path(dir), data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", Path(dir), err)
	}
	return nil
}

// Settings is the subset of Config an admin may change at runtime from the
// Settings screen, instead of hand-editing config.yaml and restarting.
// Everything else on Config — server.*, oidc.* — is deliberately absent; see
// the package doc for why.
type Settings struct {
	App  App
	Sync Sync
	FX   FX
}

// SettingsOf extracts the editable subset of a loaded Config, so a caller
// building the settings screen's current values does not need to know its
// shape independently.
func SettingsOf(c *Config) Settings { return Settings{App: c.App, Sync: c.Sync, FX: c.FX} }

// ValidateSettings checks a Settings on its own — the App/Sync/FX subset of
// what Config.Validate checks, with no dependency on Server or OIDC — so the
// settings API can reject a bad value with a 400 before ever touching disk,
// rather than only finding out from UpdateSettings after it has already
// tried to write.
func ValidateSettings(s Settings) error {
	return (&Config{App: s.App, Sync: s.Sync, FX: s.FX}).Validate()
}

// UpdateSettings applies s on top of what config.yaml currently holds on
// disk — never on top of the in-memory, env-overlaid Config a caller might
// otherwise have to hand, which is exactly what must not happen: an OIDC
// client secret supplied only by MONEYFLY_OIDC_<ID>_CLIENT_SECRET lives in
// that in-memory copy and must never be written to the file. Re-reading the
// file fresh is what keeps server.* and oidc.* untouched by this path
// entirely, secret or not.
//
// It returns the new effective Config — settings applied, environment
// re-overlaid — for the caller to swap into whatever is currently serving
// requests.
func UpdateSettings(dir string, s Settings) (*Config, error) {
	data, err := os.ReadFile(Path(dir))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", Path(dir), err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", Path(dir), err)
	}
	// Transport is resolved before validating, not after: `base_url` is
	// required once any OIDC provider is configured, and it does not live in
	// this file any more — so without this, saving an unrelated setting on an
	// OIDC instance would fail on a field the operator cannot even see here.
	// Safe this early only because Server is `yaml:"-"`; the OIDC half of the
	// environment stays after the save, where it cannot be written.
	cfg.adoptLegacyServerBlock()
	cfg.applyServerEnv()

	cfg.App, cfg.Sync, cfg.FX = s.App, s.Sync, s.FX
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := save(dir, cfg); err != nil {
		return nil, err
	}
	cfg.applyOIDCEnv()
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
	if c.FX.Enabled == nil {
		on := DefaultFXEnabled
		c.FX.Enabled = &on
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
//
// Note what is *not* here: MONEYFLY_REGISTRATION. Registration is a Settings
// field, and a field that both the Settings screen and the environment can set
// has no single owner — the symptom was a dropdown that accepted a change,
// reported "saved and applied", and showed the old value again, while the
// environment's value was quietly written into config.yaml as a side effect of
// saving something else entirely. It is a first-boot seed now instead; see
// seedFromEnv.
func (c *Config) applyEnv() {
	c.applyServerEnv()
	c.applyOIDCEnv()
}

// applyServerEnv fills the transport settings. Safe to call at any point,
// including before a save, because Server is `yaml:"-"` and so cannot be
// written to the file no matter what it holds — which is exactly why the
// settings write path can afford to resolve it early enough to validate
// against.
func (c *Config) applyServerEnv() {
	if v := os.Getenv(envAddr); v != "" {
		c.Server.Addr = v
	}
	if v := os.Getenv(envBaseURL); v != "" {
		c.Server.BaseURL = v
	}
}

// applyOIDCEnv fills provider credentials. Unlike applyServerEnv this must
// never run before a save: the oidc section *is* marshalled, so an env-supplied
// client secret applied too early would be written straight into config.yaml.
func (c *Config) applyOIDCEnv() {
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
	// Session lifetime is a security boundary, and this is a settable field —
	// the browser's own min="1" is not a check, it is a hint. A zero or negative
	// value here becomes a zero-or-past session expiry at startSession, i.e. an
	// instance nobody can stay signed in to.
	if c.App.SessionTTLDays < 1 {
		return fmt.Errorf("app.session_ttl_days: want >= 1, got %d", c.App.SessionTTLDays)
	}
	if c.App.SessionTTLDays > MaxSessionTTLDays {
		return fmt.Errorf("app.session_ttl_days: want <= %d, got %d",
			MaxSessionTTLDays, c.App.SessionTTLDays)
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
