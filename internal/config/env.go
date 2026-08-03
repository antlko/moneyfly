package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// EnvPrefix namespaces every environment override.
const EnvPrefix = "MONEYAPP_"

// applyEnv overlays environment variables onto the loaded YAML, one key at a
// time, so an operator can override a single value without templating a file.
// Secrets are environment-only: MONEYAPP_BOOTSTRAP_PASSWORD has no YAML key.
func (c *Config) applyEnv() error {
	var bad []string
	str := func(key string, dst *string) {
		if v, ok := lookup(key); ok {
			*dst = v
		}
	}
	integer := func(key string, dst *int) {
		if v, ok := lookup(key); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				bad = append(bad, fmt.Sprintf("%s%s: %q is not an integer", EnvPrefix, key, v))
				return
			}
			*dst = n
		}
	}
	integer64 := func(key string, dst *int64) {
		if v, ok := lookup(key); ok {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				bad = append(bad, fmt.Sprintf("%s%s: %q is not an integer", EnvPrefix, key, v))
				return
			}
			*dst = n
		}
	}
	float := func(key string, dst *float64) {
		if v, ok := lookup(key); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				bad = append(bad, fmt.Sprintf("%s%s: %q is not a number", EnvPrefix, key, v))
				return
			}
			*dst = f
		}
	}
	boolean := func(key string, dst *bool) {
		if v, ok := lookup(key); ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				bad = append(bad, fmt.Sprintf("%s%s: %q is not a boolean", EnvPrefix, key, v))
				return
			}
			*dst = b
		}
	}
	duration := func(key string, dst *time.Duration) {
		if v, ok := lookup(key); ok {
			d, err := time.ParseDuration(v)
			if err != nil {
				bad = append(bad, fmt.Sprintf("%s%s: %q is not a duration", EnvPrefix, key, v))
				return
			}
			*dst = d
		}
	}

	str("SERVER_ADDR", &c.Server.Addr)
	str("SERVER_BASE_URL", &c.Server.BaseURL)

	str("DATABASE_PATH", &c.Database.Path)
	integer("DATABASE_MAX_OPEN_CONNS", &c.Database.MaxOpenConns)
	integer("DATABASE_MAX_IDLE_CONNS", &c.Database.MaxIdleConns)
	integer("DATABASE_BUSY_TIMEOUT_MS", &c.Database.BusyTimeoutMS)

	duration("AUTH_SESSION_TTL", &c.Auth.SessionTTL)
	integer("AUTH_BCRYPT_COST", &c.Auth.BcryptCost)
	str("AUTH_BOOTSTRAP_ADMIN_EMAIL", &c.Auth.BootstrapAdminEmail)
	integer("AUTH_LOGIN_RATE_LIMIT", &c.Auth.LoginRateLimit)
	duration("AUTH_LOGIN_RATE_WINDOW", &c.Auth.LoginRateWindow)
	// Secret: environment only, never a config-file or source default.
	str("BOOTSTRAP_PASSWORD", &c.Auth.BootstrapPassword)

	boolean("TELEGRAM_ENABLED", &c.Telegram.Enabled)
	str("TELEGRAM_TOKEN", &c.Telegram.Token)
	integer64("TELEGRAM_MAX_FILE_BYTES", &c.Telegram.MaxFileBytes)

	str("IMPORTS_UPLOAD_DIR", &c.Imports.UploadDir)
	integer64("IMPORTS_MAX_UPLOAD_BYTES", &c.Imports.MaxUploadBytes)

	str("PROVIDERS_FX_PRIMARY", &c.Providers.FX.Primary)
	str("PROVIDERS_FX_FALLBACK", &c.Providers.FX.Fallback)
	duration("PROVIDERS_FX_REFRESH", &c.Providers.FX.Refresh)
	float("PROVIDERS_FX_PLAUSIBILITY_MAX_CHANGE", &c.Providers.FX.PlausibilityMaxChange)

	boolean("BACKUP_ENABLED", &c.Backup.Enabled)
	str("BACKUP_SCHEDULE", &c.Backup.Schedule)
	integer("BACKUP_KEEP", &c.Backup.Keep)
	str("BACKUP_PATH", &c.Backup.Path)

	str("LOG_LEVEL", &c.Log.Level)
	str("LOG_FORMAT", &c.Log.Format)
	boolean("LOG_MASK_PII", &c.Log.MaskPII)

	str("REPORTS_BASE_CURRENCY", &c.Reports.BaseCurrency)
	float("REPORTS_WARN_PERCENT", &c.Reports.WarnPercent)
	float("REPORTS_OVER_MULTIPLIER", &c.Reports.OverMultiplier)

	c.Reports.BaseCurrency = strings.ToUpper(strings.TrimSpace(c.Reports.BaseCurrency))

	if len(bad) > 0 {
		return &Error{Problems: bad}
	}
	return nil
}

func lookup(key string) (string, bool) {
	v, ok := os.LookupEnv(EnvPrefix + key)
	if !ok {
		return "", false
	}
	return v, true
}
