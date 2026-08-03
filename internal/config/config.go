// Package config loads and validates configuration: config.yaml as the
// authoritative source, environment variables overriding individual keys,
// secrets from the environment only.
//
// Validation happens at boot and reports every problem, naming the offending
// key, so the process refuses to start rather than failing hours later.
// See docs/10-deployment-ci.md §10.3.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the whole configuration surface.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	Auth      AuthConfig      `yaml:"auth"`
	Telegram  TelegramConfig  `yaml:"telegram"`
	Imports   ImportsConfig   `yaml:"imports"`
	Providers ProvidersConfig `yaml:"providers"`
	Backup    BackupConfig    `yaml:"backup"`
	Log       LogConfig       `yaml:"log"`
	// Reports carries the threshold defaults used by the budget report until the
	// per-user setting table lands in stage 07 (docs/07-metrics-and-budgets.md §7.6).
	Reports ReportsConfig `yaml:"reports"`
}

// ServerConfig is the HTTP listener.
type ServerConfig struct {
	Addr    string `yaml:"addr"`
	BaseURL string `yaml:"base_url"`
}

// DatabaseConfig is the SQLite file and pool. Writes serialise in SQLite, so a
// large pool only adds contention.
type DatabaseConfig struct {
	Path          string `yaml:"path"`
	MaxOpenConns  int    `yaml:"max_open_conns"`
	MaxIdleConns  int    `yaml:"max_idle_conns"`
	BusyTimeoutMS int    `yaml:"busy_timeout_ms"`
}

// AuthConfig covers sessions and the bootstrap admin.
type AuthConfig struct {
	SessionTTL          time.Duration `yaml:"session_ttl"`
	BcryptCost          int           `yaml:"bcrypt_cost"`
	BootstrapAdminEmail string        `yaml:"bootstrap_admin_email"`
	// BootstrapPassword comes from MONEYAPP_BOOTSTRAP_PASSWORD only. It has no
	// YAML key: a secret must never have a source-code or config-file default.
	BootstrapPassword string        `yaml:"-"`
	LoginRateLimit    int           `yaml:"login_rate_limit"`
	LoginRateWindow   time.Duration `yaml:"login_rate_window"`
}

// TelegramConfig is opt-in and off by default (docs/adr/0013-telegram-in-process.md).
type TelegramConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Token        string `yaml:"token"`
	MaxFileBytes int64  `yaml:"max_file_bytes"`
}

// ImportsConfig covers the Monefy import: where raw uploads are retained and how
// large one may be (docs/04-import-monefy.md §4.13).
type ImportsConfig struct {
	// UploadDir retains the raw file so a batch can be reprocessed after an
	// importer fix without re-exporting from the phone.
	UploadDir      string `yaml:"upload_dir"`
	MaxUploadBytes int64  `yaml:"max_upload_bytes"`
}

// ProvidersConfig configures the outbound rate providers.
type ProvidersConfig struct {
	FX FXProviderConfig `yaml:"fx"`
}

// FXProviderConfig is the provider chain and its guard rails.
type FXProviderConfig struct {
	Primary               string        `yaml:"primary"`
	Fallback              string        `yaml:"fallback"`
	Refresh               time.Duration `yaml:"refresh"`
	PlausibilityMaxChange float64       `yaml:"plausibility_max_change"`
}

// BackupConfig is the nightly SQLite snapshot.
type BackupConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Schedule string `yaml:"schedule"`
	Keep     int    `yaml:"keep"`
	// Path is where archives are written. Empty means a `backups` directory
	// beside the database, which is the mounted volume in every deployment.
	Path string `yaml:"path"`
}

// BackupDir is the directory backups are written to.
func (c Config) BackupDir() string {
	if strings.TrimSpace(c.Backup.Path) != "" {
		return c.Backup.Path
	}
	return filepath.Join(filepath.Dir(c.Database.Path), "backups")
}

// LogConfig configures the logger.
type LogConfig struct {
	Level   string `yaml:"level"`
	Format  string `yaml:"format"`
	MaskPII bool   `yaml:"mask_pii"`
}

// ReportsConfig holds report defaults.
type ReportsConfig struct {
	BaseCurrency string `yaml:"base_currency"`
	// WarnPercent is the workbook's C2 "Percent for yellow": 90-100% of plan is amber.
	WarnPercent float64 `yaml:"warn_percent"`
	// OverMultiplier was hardcoded 2x in the workbook.
	OverMultiplier float64 `yaml:"over_multiplier"`
}

// Default returns the built-in configuration, matching config.yaml.dist except
// for secrets, which have no defaults.
func Default() Config {
	return Config{
		Server: ServerConfig{Addr: ":8080", BaseURL: "http://localhost:8080"},
		Database: DatabaseConfig{
			Path: "/config/moneyapp.db", MaxOpenConns: 4, MaxIdleConns: 2, BusyTimeoutMS: 5000,
		},
		Auth: AuthConfig{
			SessionTTL: 720 * time.Hour, BcryptCost: 12,
			LoginRateLimit: 5, LoginRateWindow: 15 * time.Minute,
		},
		Telegram: TelegramConfig{Enabled: false, MaxFileBytes: 20971520},
		Imports:  ImportsConfig{UploadDir: "/config/uploads", MaxUploadBytes: 8388608},
		Providers: ProvidersConfig{FX: FXProviderConfig{
			Primary: "open-er-api", Fallback: "fawazahmed0",
			Refresh: 24 * time.Hour, PlausibilityMaxChange: 0.15,
		}},
		Backup:  BackupConfig{Enabled: true, Schedule: "0 3 * * *", Keep: 14},
		Log:     LogConfig{Level: "info", Format: "json", MaskPII: true},
		Reports: ReportsConfig{BaseCurrency: "EUR", WarnPercent: 10, OverMultiplier: 2},
	}
}

// Load reads path (missing file is not an error — defaults plus environment are
// a valid configuration), applies environment overrides, then validates.
func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
		switch {
		case err == nil:
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				return Config{}, fmt.Errorf("config: parsing %s: %w", path, err)
			}
		case errors.Is(err, os.ErrNotExist):
			// Fall through: defaults + environment.
		default:
			return Config{}, fmt.Errorf("config: reading %s: %w", path, err)
		}
	}
	if err := cfg.applyEnv(); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Error lists every configuration problem found, so one boot attempt reveals
// all of them rather than one per restart.
type Error struct{ Problems []string }

// Error implements error.
func (e *Error) Error() string {
	return "config: " + strconv.Itoa(len(e.Problems)) + " problem(s):\n  - " + strings.Join(e.Problems, "\n  - ")
}

// Validate returns every problem, not just the first.
func (c Config) Validate() error {
	var p []string
	add := func(key, format string, args ...any) {
		p = append(p, key+": "+fmt.Sprintf(format, args...))
	}

	if strings.TrimSpace(c.Server.Addr) == "" {
		add("server.addr", "must not be empty")
	}
	if u, err := url.Parse(c.Server.BaseURL); err != nil || !u.IsAbs() || u.Host == "" {
		add("server.base_url", "must be an absolute URL including scheme and host, got %q", c.Server.BaseURL)
	}

	if strings.TrimSpace(c.Database.Path) == "" {
		add("database.path", "must not be empty")
	} else if err := checkWritable(c.Database.Path); err != nil {
		add("database.path", "%v", err)
	}
	if c.Database.MaxOpenConns < 1 {
		add("database.max_open_conns", "must be at least 1, got %d", c.Database.MaxOpenConns)
	}
	if c.Database.MaxIdleConns < 0 {
		add("database.max_idle_conns", "must not be negative, got %d", c.Database.MaxIdleConns)
	}
	if c.Database.MaxIdleConns > c.Database.MaxOpenConns {
		add("database.max_idle_conns", "must not exceed max_open_conns (%d > %d)",
			c.Database.MaxIdleConns, c.Database.MaxOpenConns)
	}
	if c.Database.BusyTimeoutMS < 0 {
		add("database.busy_timeout_ms", "must not be negative, got %d", c.Database.BusyTimeoutMS)
	}

	if c.Auth.BcryptCost < 10 {
		add("auth.bcrypt_cost", "must be at least 10, got %d", c.Auth.BcryptCost)
	}
	if c.Auth.BcryptCost > 31 {
		add("auth.bcrypt_cost", "must be at most 31, got %d", c.Auth.BcryptCost)
	}
	if c.Auth.SessionTTL <= 0 {
		add("auth.session_ttl", "must be a positive duration, got %s", c.Auth.SessionTTL)
	}
	if c.Auth.BootstrapAdminEmail != "" && !strings.Contains(c.Auth.BootstrapAdminEmail, "@") {
		add("auth.bootstrap_admin_email", "must be an email address, got %q", c.Auth.BootstrapAdminEmail)
	}
	if c.Auth.LoginRateLimit < 1 {
		add("auth.login_rate_limit", "must be at least 1, got %d", c.Auth.LoginRateLimit)
	}
	if c.Auth.LoginRateWindow <= 0 {
		add("auth.login_rate_window", "must be a positive duration, got %s", c.Auth.LoginRateWindow)
	}

	if c.Telegram.Enabled && strings.TrimSpace(c.Telegram.Token) == "" {
		add("telegram.token", "must be set when telegram.enabled is true (use MONEYAPP_TELEGRAM_TOKEN)")
	}
	if c.Telegram.MaxFileBytes <= 0 {
		add("telegram.max_file_bytes", "must be positive, got %d", c.Telegram.MaxFileBytes)
	}

	if strings.TrimSpace(c.Imports.UploadDir) == "" {
		add("imports.upload_dir", "must not be empty")
	}
	if c.Imports.MaxUploadBytes <= 0 {
		add("imports.max_upload_bytes", "must be positive, got %d", c.Imports.MaxUploadBytes)
	}

	if c.Providers.FX.Refresh <= 0 {
		add("providers.fx.refresh", "must be a positive duration, got %s", c.Providers.FX.Refresh)
	}
	if c.Providers.FX.PlausibilityMaxChange <= 0 || c.Providers.FX.PlausibilityMaxChange > 1 {
		add("providers.fx.plausibility_max_change", "must be in (0,1], got %v", c.Providers.FX.PlausibilityMaxChange)
	}
	if strings.TrimSpace(c.Providers.FX.Primary) == "" {
		add("providers.fx.primary", "must name a provider")
	}

	if c.Backup.Enabled && c.Backup.Keep < 1 {
		add("backup.keep", "must be at least 1 when backups are enabled, got %d", c.Backup.Keep)
	}
	if c.Backup.Enabled && len(strings.Fields(c.Backup.Schedule)) != 5 {
		add("backup.schedule", "must be a 5-field cron expression, got %q", c.Backup.Schedule)
	}

	switch strings.ToLower(c.Log.Level) {
	case "debug", "info", "warn", "warning", "error":
	default:
		add("log.level", "must be one of debug|info|warn|error, got %q", c.Log.Level)
	}
	switch strings.ToLower(c.Log.Format) {
	case "json", "text":
	default:
		add("log.format", "must be json or text, got %q", c.Log.Format)
	}

	if len(c.Reports.BaseCurrency) != 3 {
		add("reports.base_currency", "must be a 3-letter ISO-4217 code, got %q", c.Reports.BaseCurrency)
	}
	if c.Reports.WarnPercent < 0 || c.Reports.WarnPercent >= 100 {
		add("reports.warn_percent", "must be in [0,100), got %v", c.Reports.WarnPercent)
	}
	if c.Reports.OverMultiplier <= 1 {
		add("reports.over_multiplier", "must be greater than 1, got %v", c.Reports.OverMultiplier)
	}

	if len(p) > 0 {
		return &Error{Problems: p}
	}
	return nil
}

// checkWritable verifies the database can actually be created or opened for
// writing, which is the failure operators hit most often with a mounted volume.
func checkWritable(path string) error {
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("directory %s does not exist", dir)
		}
		return fmt.Errorf("directory %s is not usable: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	if st, err := os.Stat(path); err == nil {
		if st.IsDir() {
			return fmt.Errorf("%s is a directory, not a database file", path)
		}
		f, err := os.OpenFile(path, os.O_WRONLY, 0o600) //nolint:gosec // operator-supplied path
		if err != nil {
			return fmt.Errorf("%s exists but is not writable", path)
		}
		return f.Close()
	}
	probe := filepath.Join(dir, ".moneyapp-write-probe")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // operator-supplied path
	if err != nil {
		return fmt.Errorf("directory %s is not writable", dir)
	}
	_ = f.Close()
	return os.Remove(probe)
}
