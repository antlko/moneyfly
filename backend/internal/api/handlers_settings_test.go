package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"moneyfly/internal/config"
)

func settingsBody(t *testing.T, s settingsDTO) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	return string(b)
}

func TestSettingsRoutesRequireAuth(t *testing.T) {
	s := newTestServer(t, "")
	if res := s.do(t, "GET", "/api/admin/settings", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET: status = %d, want 401", res.StatusCode)
	}
	if res := s.do(t, "PUT", "/api/admin/settings", "{}", ""); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("PUT: status = %d, want 401", res.StatusCode)
	}
}

func TestSettingsRoutesRequireAdmin(t *testing.T) {
	s := newTestServer(t, "")
	member := signUpNonAdmin(t, s)
	if res := s.do(t, "GET", "/api/admin/settings", "", member); res.StatusCode != http.StatusForbidden {
		t.Errorf("GET: status = %d, want 403", res.StatusCode)
	}
	if res := s.do(t, "PUT", "/api/admin/settings", "{}", member); res.StatusCode != http.StatusForbidden {
		t.Errorf("PUT: status = %d, want 403", res.StatusCode)
	}
}

func TestGetSettingsReturnsDefaults(t *testing.T) {
	s := newTestServer(t, "")
	admin := signUp(t, s, "admin@example.com", "dev-admin")

	got := decodeBody[settingsDTO](t, s.do(t, "GET", "/api/admin/settings", "", admin))
	if got.Registration != config.RegistrationOpen {
		t.Errorf("registration = %q, want %q", got.Registration, config.RegistrationOpen)
	}
	if got.DefaultCurrency != config.DefaultCurrency {
		t.Errorf("defaultCurrency = %q, want %q", got.DefaultCurrency, config.DefaultCurrency)
	}
	if got.SessionTTLDays != config.DefaultSessionTTLDays {
		t.Errorf("sessionTtlDays = %d, want %d", got.SessionTTLDays, config.DefaultSessionTTLDays)
	}
	if got.ChangeLogRetentionDays != config.DefaultChangeLogRetentionDays {
		t.Errorf("changeLogRetentionDays = %d, want %d",
			got.ChangeLogRetentionDays, config.DefaultChangeLogRetentionDays)
	}
	if !got.FXEnabled {
		t.Error("fxEnabled = false, want true (the documented on-by-default)")
	}
	if got.FXRefreshAt != config.DefaultFXRefreshAt {
		t.Errorf("fxRefreshAt = %q, want %q", got.FXRefreshAt, config.DefaultFXRefreshAt)
	}
	if len(got.FXProviders) == 0 {
		t.Error("fxProviders is empty, want the default chain")
	}
}

// The point of the whole feature: a change made through the API must be
// visible immediately — no restart — and must survive one, i.e. actually
// land in config.yaml rather than only living in the running process.
func TestUpdateSettingsAppliesLiveAndPersists(t *testing.T) {
	s := newTestServer(t, "")
	admin := signUp(t, s, "admin@example.com", "dev-admin")

	current := decodeBody[settingsDTO](t, s.do(t, "GET", "/api/admin/settings", "", admin))
	current.Registration = config.RegistrationClosed
	current.FXRefreshAt = "05:30"

	res := s.do(t, "PUT", "/api/admin/settings", settingsBody(t, current), admin)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d", res.StatusCode)
	}
	updated := decodeBody[settingsDTO](t, res)
	if updated.Registration != config.RegistrationClosed || updated.FXRefreshAt != "05:30" {
		t.Fatalf("response = %+v, want the new values echoed back", updated)
	}

	// Live: registration is enforced straight from s.config(), so signing up
	// a second account must now be refused without any restart.
	if res := s.do(t, "POST", "/api/auth/register",
		`{"email":"second@example.com","password":"hunter2hunter2"}`, ""); res.StatusCode == http.StatusCreated {
		t.Error("registration still open after PUT closed it")
	}

	// Persisted: a fresh Load of the same config dir must see it too, proving
	// it reached config.yaml and not just the in-memory copy.
	reloaded, err := config.Load(s.configDir)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if reloaded.App.Registration != config.RegistrationClosed {
		t.Errorf("reloaded registration = %q, want closed", reloaded.App.Registration)
	}
	if reloaded.FX.RefreshAt != "05:30" {
		t.Errorf("reloaded fx.refresh_at = %q, want 05:30", reloaded.FX.RefreshAt)
	}
}

func TestUpdateSettingsRejectsInvalidAndLeavesCurrentValueAlone(t *testing.T) {
	s := newTestServer(t, "")
	admin := signUp(t, s, "admin@example.com", "dev-admin")

	current := decodeBody[settingsDTO](t, s.do(t, "GET", "/api/admin/settings", "", admin))
	bad := current
	bad.Registration = "sometimes"

	res := s.do(t, "PUT", "/api/admin/settings", settingsBody(t, bad), admin)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}

	still := decodeBody[settingsDTO](t, s.do(t, "GET", "/api/admin/settings", "", admin))
	if still.Registration != current.Registration {
		t.Errorf("registration = %q after a rejected update, want it unchanged (%q)",
			still.Registration, current.Registration)
	}
}

// A structural guard on top of internal/config's own tests: the settings
// response must not even have a place an OIDC secret, or any server.*
// value, could end up.
func TestSettingsResponseCarriesNothingServerOrOIDC(t *testing.T) {
	s := newTestServer(t, `
server:
  base_url: https://money.example.com
oidc:
  - id: my-idp
    issuer: https://idp.example.com
    client_id: abc
    client_secret: s3cret-value
`)
	admin := signUp(t, s, "admin@example.com", "dev-admin")

	res := s.do(t, "GET", "/api/admin/settings", "", admin)
	body := decodeBody[json.RawMessage](t, res)
	if strings.Contains(string(body), "s3cret") || strings.Contains(string(body), "money.example.com") {
		t.Errorf("settings response leaked server/oidc data: %s", body)
	}
}
