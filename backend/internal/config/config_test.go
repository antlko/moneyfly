package config

import (
	"os"
	"path/filepath"
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
	dir := write(t, "server:\n  addr: \":8080\"\n")
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
