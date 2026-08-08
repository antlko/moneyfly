package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return dir
}

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != DefaultAddr {
		t.Errorf("addr = %q, want %q", cfg.Server.Addr, DefaultAddr)
	}
	if cfg.App.Registration != RegistrationOpen {
		t.Errorf("registration = %q, want %q", cfg.App.Registration, RegistrationOpen)
	}
	if cfg.Sync.ChangeLogRetentionDays != DefaultChangeLogRetentionDays {
		t.Errorf("retention = %d, want %d",
			cfg.Sync.ChangeLogRetentionDays, DefaultChangeLogRetentionDays)
	}
}

// A value the operator set must survive normalize(): defaults fill zeros only.
func TestLoadKeepsExplicitValues(t *testing.T) {
	dir := write(t, `
server:
  addr: "127.0.0.1:9999"
app:
  registration: closed
  default_currency: UAH
sync:
  change_log_retention_days: 7
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != "127.0.0.1:9999" {
		t.Errorf("addr = %q", cfg.Server.Addr)
	}
	if cfg.App.Registration != RegistrationClosed {
		t.Errorf("registration = %q", cfg.App.Registration)
	}
	if cfg.App.DefaultCurrency != "UAH" {
		t.Errorf("currency = %q", cfg.App.DefaultCurrency)
	}
	if cfg.Sync.ChangeLogRetentionDays != 7 {
		t.Errorf("retention = %d", cfg.Sync.ChangeLogRetentionDays)
	}
}

func TestLoadEnvOverridesFile(t *testing.T) {
	// Deliberately not DefaultAddr: if the file said the same thing as the
	// default, this would pass even if the file were ignored entirely.
	dir := write(t, "server:\n  addr: \":9999\"\n")
	t.Setenv("MONEYFLY_ADDR", ":7070")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":7070" {
		t.Errorf("addr = %q, want env value", cfg.Server.Addr)
	}
}

// The whole point of the env overlay: a client secret can stay out of the file.
func TestLoadOIDCSecretFromEnv(t *testing.T) {
	dir := write(t, `
server:
  base_url: https://money.example.com
oidc:
  - id: my-idp
    issuer: https://idp.example.com
    client_id: abc
`)
	t.Setenv("MONEYFLY_OIDC_MY_IDP_CLIENT_SECRET", "s3cret")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p := cfg.Provider("my-idp")
	if p == nil {
		t.Fatal("provider my-idp not found")
	}
	if p.ClientSecret != "s3cret" {
		t.Errorf("client_secret = %q, want the env value", p.ClientSecret)
	}
	if p.Name != "my-idp" {
		t.Errorf("name = %q, want it defaulted from id", p.Name)
	}
	if len(p.Scopes) == 0 {
		t.Error("scopes should be defaulted")
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{"bad registration", "app:\n  registration: sometimes\n"},
		{"short currency", "app:\n  default_currency: EU\n"},
		{"zero retention", "sync:\n  change_log_retention_days: -1\n"},
		{"oidc without issuer", "server:\n  base_url: http://x\noidc:\n  - id: a\n    client_id: b\n"},
		{"oidc without base_url", "oidc:\n  - id: a\n    issuer: http://i\n    client_id: b\n"},
		{"duplicate oidc id", "server:\n  base_url: http://x\noidc:\n" +
			"  - {id: a, issuer: 'http://i', client_id: b}\n" +
			"  - {id: a, issuer: 'http://i', client_id: c}\n"},
		// A typo'd provider id used to be skipped in silence, and the symptom
		// was rates that quietly stopped updating.
		{"unknown fx provider", "fx:\n  providers: [ecb]\n"},
		{"fx refresh_at not a time", "fx:\n  refresh_at: 'lunchtime'\n"},
		{"fx refresh_at out of range", "fx:\n  refresh_at: '25:00'\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(write(t, tt.yaml)); err == nil {
				t.Error("want an error, got nil")
			}
		})
	}
}

func TestPaths(t *testing.T) {
	if got, want := Path("/c"), filepath.Join("/c", "config.yaml"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	if got, want := DBPath("/c"), filepath.Join("/c", "moneyfly.db"); got != want {
		t.Errorf("DBPath = %q, want %q", got, want)
	}
}

// An instance with no config.yaml — the documented way to run this from a bare
// `docker run` — must still fetch rates. It did not: `enabled` was a plain bool
// whose zero value is false, so every foreign-currency record sat outside the
// totals forever, captioned "no exchange rate yet".
func TestFXIsOnWhenUnconfigured(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want bool
	}{
		{"no file at all", "", true},
		{"fx section absent", "app:\n  default_currency: EUR\n", true},
		{"fx present but silent about enabled", "fx:\n  refresh_at: '05:00'\n", true},
		{"explicitly on", "fx:\n  enabled: true\n", true},
		{"explicitly off is still honoured", "fx:\n  enabled: false\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.yaml != "" {
				dir = write(t, tc.yaml)
			}
			cfg, err := Load(dir)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.FX.On(); got != tc.want {
				t.Errorf("FX.On() = %v, want %v", got, tc.want)
			}
		})
	}
}

// A bare `docker run` against an empty volume must leave behind a
// config.yaml that shows every field there is to adjust, not just an
// in-memory default an operator has no way to discover short of reading the
// source.
func TestLoadMissingFileWritesFullDefaultFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := os.Stat(Path(dir)); !os.IsNotExist(err) {
		t.Fatalf("config.yaml already exists before Load: %v", err)
	}

	if _, err := Load(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}

	written, err := Load(dir) // re-read from disk, not the first call's in-memory copy
	if err != nil {
		t.Fatalf("re-Load after bootstrap: %v", err)
	}
	if written.Server.Addr != DefaultAddr {
		t.Errorf("addr = %q, want %q", written.Server.Addr, DefaultAddr)
	}
	if written.App.Registration != RegistrationOpen {
		t.Errorf("registration = %q, want %q", written.App.Registration, RegistrationOpen)
	}
	if written.Sync.ChangeLogRetentionDays != DefaultChangeLogRetentionDays {
		t.Errorf("retention = %d, want %d", written.Sync.ChangeLogRetentionDays, DefaultChangeLogRetentionDays)
	}
	if !written.FX.On() || written.FX.RefreshAt != DefaultFXRefreshAt || len(written.FX.Providers) == 0 {
		t.Errorf("fx = %+v, want the full set of defaults written out", written.FX)
	}
}

// Environment variables must win at every Load, which means they must never
// be the thing captured in the bootstrap file — otherwise a value only ever
// meant to override this one run would quietly become the new on-disk
// default forever.
func TestLoadDoesNotBakeEnvIntoBootstrapFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MONEYFLY_ADDR", ":7070")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":7070" {
		t.Fatalf("effective addr = %q, want the env value", cfg.Server.Addr)
	}

	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if strings.Contains(string(raw), "7070") {
		t.Errorf("the env-only addr leaked into the written file:\n%s", raw)
	}
	if !strings.Contains(string(raw), DefaultAddr) {
		t.Errorf("the written file does not contain the real default %q:\n%s", DefaultAddr, raw)
	}
}

// The whole reason UpdateSettings re-reads config.yaml from disk instead of
// taking a Config from the caller: an OIDC client secret supplied only by
// environment lives in the in-memory copy, and must never be written out.
func TestUpdateSettingsNeverWritesAnEnvOnlySecret(t *testing.T) {
	dir := write(t, `
server:
  base_url: https://money.example.com
oidc:
  - id: my-idp
    issuer: https://idp.example.com
    client_id: abc
`)
	t.Setenv("MONEYFLY_OIDC_MY_IDP_CLIENT_SECRET", "s3cret-value")

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Provider("my-idp").ClientSecret != "s3cret-value" {
		t.Fatalf("effective client_secret not applied from env")
	}

	settings := SettingsOf(cfg)
	settings.App.Registration = RegistrationClosed
	updated, err := UpdateSettings(dir, settings)
	if err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if updated.App.Registration != RegistrationClosed {
		t.Errorf("returned config registration = %q, want closed", updated.App.Registration)
	}
	// server.* and oidc.* must survive untouched.
	if updated.Server.BaseURL != "https://money.example.com" {
		t.Errorf("base_url = %q, want it preserved", updated.Server.BaseURL)
	}
	if updated.Provider("my-idp") == nil {
		t.Fatal("oidc provider dropped by UpdateSettings")
	}

	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if strings.Contains(string(raw), "s3cret-value") {
		t.Errorf("UpdateSettings wrote the env-only client secret to disk:\n%s", raw)
	}

	// And the persisted change must actually be there for the next boot.
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("reload after UpdateSettings: %v", err)
	}
	if reloaded.App.Registration != RegistrationClosed {
		t.Errorf("registration did not persist: got %q", reloaded.App.Registration)
	}
}

// An invalid settings change must be rejected without touching the file —
// the operator's existing, working config must not be overwritten by
// something that would fail Validate on the next restart.
func TestUpdateSettingsRejectsInvalidWithoutWriting(t *testing.T) {
	dir := write(t, "app:\n  registration: open\n")
	before, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	settings := SettingsOf(cfg)
	settings.App.Registration = "sometimes"
	if _, err := UpdateSettings(dir, settings); err == nil {
		t.Fatal("want an error for an invalid registration mode, got nil")
	}

	after, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("file changed despite a rejected update:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// The default provider chain has to be the one Validate accepts, or a config
// that names nothing would fail on the values this package itself supplied.
func TestFXDefaults(t *testing.T) {
	cfg, err := Load(write(t, "fx:\n  enabled: true\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.FX.Providers) != len(FXProviders) {
		t.Fatalf("providers = %v, want the default chain %v", cfg.FX.Providers, FXProviders)
	}
	if cfg.FX.RefreshAt != DefaultFXRefreshAt {
		t.Errorf("refresh_at = %q, want %q", cfg.FX.RefreshAt, DefaultFXRefreshAt)
	}
	hour, minute, err := cfg.FX.RefreshHourMinute()
	if err != nil || hour != 4 || minute != 0 {
		t.Errorf("RefreshHourMinute = %d:%d, %v; want 4:0, nil", hour, minute, err)
	}
}
