package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestConfig_LoadsYAMLAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
server:
  addr: ":9090"
  base_url: "https://money.example.com"
database:
  path: "`+filepath.Join(dir, "moneyapp.db")+`"
auth:
  bcrypt_cost: 11
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":9090" {
		t.Fatalf("addr = %q", cfg.Server.Addr)
	}
	if cfg.Auth.BcryptCost != 11 {
		t.Fatalf("bcrypt_cost = %d", cfg.Auth.BcryptCost)
	}
	// Untouched keys keep their defaults.
	if cfg.Database.MaxOpenConns != 4 {
		t.Fatalf("max_open_conns = %d, want the default 4", cfg.Database.MaxOpenConns)
	}
	if !cfg.Log.MaskPII {
		t.Fatal("mask_pii must default to true")
	}
	if cfg.Reports.WarnPercent != 10 || cfg.Reports.OverMultiplier != 2 {
		t.Fatalf("threshold defaults wrong: %+v", cfg.Reports)
	}
}

func TestConfig_RejectsEnabledBotWithoutToken(t *testing.T) {
	dir := t.TempDir()
	cfg := Default()
	cfg.Database.Path = filepath.Join(dir, "moneyapp.db")
	cfg.Telegram.Enabled = true
	cfg.Telegram.Token = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("an enabled bot with no token must not validate")
	}
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *config.Error, got %T", err)
	}
	if !strings.Contains(err.Error(), "telegram.token") {
		t.Fatalf("the error must name the offending key, got: %v", err)
	}
}

func TestConfig_ReportsAllProblems(t *testing.T) {
	cfg := Default()
	cfg.Database.Path = "/nonexistent-directory-xyz/moneyapp.db"
	cfg.Auth.BcryptCost = 4
	cfg.Server.BaseURL = "money.example.com" // not absolute
	cfg.Telegram.Enabled = true
	cfg.Log.Level = "loud"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("must not validate")
	}
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *config.Error, got %T", err)
	}
	for _, key := range []string{
		"database.path", "auth.bcrypt_cost", "server.base_url", "telegram.token", "log.level",
	} {
		found := false
		for _, p := range cerr.Problems {
			if strings.HasPrefix(p, key+":") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("problem list must include %s; got %v", key, cerr.Problems)
		}
	}
	if len(cerr.Problems) < 5 {
		t.Fatalf("every problem must be reported, got %d: %v", len(cerr.Problems), cerr.Problems)
	}
}

func TestConfig_RejectsUnwritableDatabasePath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "readonly")
	if err := os.Mkdir(sub, 0o500); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })

	cfg := Default()
	cfg.Database.Path = filepath.Join(sub, "moneyapp.db")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "database.path") {
		t.Fatalf("an unwritable directory must be rejected, got %v", err)
	}

	cfg.Database.Path = filepath.Join(dir, "does", "not", "exist.db")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("a missing directory must be rejected, got %v", err)
	}
}

func TestConfig_EnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
server:
  addr: ":8080"
  base_url: "https://from-yaml.example.com"
database:
  path: "`+filepath.Join(dir, "moneyapp.db")+`"
telegram:
  enabled: false
`)
	t.Setenv("MONEYAPP_SERVER_ADDR", ":7000")
	t.Setenv("MONEYAPP_TELEGRAM_ENABLED", "true")
	t.Setenv("MONEYAPP_TELEGRAM_TOKEN", "123456789:AAEhBOweik6ad9r_secret_token_value_x")
	t.Setenv("MONEYAPP_BOOTSTRAP_PASSWORD", "s3cret-bootstrap")
	t.Setenv("MONEYAPP_AUTH_SESSION_TTL", "1h")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":7000" {
		t.Fatalf("env must win over yaml, got %q", cfg.Server.Addr)
	}
	if !cfg.Telegram.Enabled || cfg.Telegram.Token == "" {
		t.Fatal("telegram env overrides not applied")
	}
	if cfg.Auth.BootstrapPassword != "s3cret-bootstrap" {
		t.Fatal("bootstrap password must come from the environment")
	}
	if cfg.Auth.SessionTTL != time.Hour {
		t.Fatalf("session_ttl = %s", cfg.Auth.SessionTTL)
	}
}

func TestConfig_RejectsMalformedEnv(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, "database:\n  path: \""+filepath.Join(dir, "moneyapp.db")+"\"\n")
	t.Setenv("MONEYAPP_AUTH_BCRYPT_COST", "twelve")
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "MONEYAPP_AUTH_BCRYPT_COST") {
		t.Fatalf("a malformed override must name the variable, got %v", err)
	}
}

func TestConfig_MissingFileIsNotFatal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MONEYAPP_DATABASE_PATH", filepath.Join(dir, "moneyapp.db"))
	cfg, err := Load(filepath.Join(dir, "absent.yaml"))
	if err != nil {
		t.Fatalf("defaults plus environment must be a valid configuration: %v", err)
	}
	if cfg.Server.Addr != ":8080" {
		t.Fatalf("addr = %q", cfg.Server.Addr)
	}
}

func TestConfig_BootstrapPasswordHasNoYAMLKey(t *testing.T) {
	dir := t.TempDir()
	path := writeConfig(t, `
database:
  path: "`+filepath.Join(dir, "moneyapp.db")+`"
auth:
  bootstrap_password: "from-yaml"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Auth.BootstrapPassword != "" {
		t.Fatal("a secret must not be readable from config.yaml")
	}
}

func TestConfig_DistFileValidates(t *testing.T) {
	// The committed sample must be a working configuration once the database
	// path is pointed somewhere writable.
	dist, err := filepath.Abs(filepath.Join("..", "..", "config.yaml.dist"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if _, err := os.Stat(dist); err != nil {
		t.Skipf("config.yaml.dist not present: %v", err)
	}
	t.Setenv("MONEYAPP_DATABASE_PATH", filepath.Join(t.TempDir(), "moneyapp.db"))
	if _, err := Load(dist); err != nil {
		t.Fatalf("config.yaml.dist must validate: %v", err)
	}
}
