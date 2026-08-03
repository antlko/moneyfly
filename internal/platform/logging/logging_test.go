package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

type loginAttempt struct {
	Email    string
	Password string
}

func TestLog_MasksSecrets(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, Options{Level: "debug", Format: "json", MaskPII: true})

	log.Info("login", "password", "hunter2")
	log.Info("login attempt", "attempt", loginAttempt{Email: "a@b.test", Password: "hunter2"})
	log.Info("download", "url", "https://api.telegram.org/file/bot123456789:AAEhBOweik6ad9r_QXbLXyz1234567890abc/documents/file.csv")
	log.Info("config", slog.Group("auth", slog.String("bootstrap_password", "hunter2")))
	log.Info("query string: ?api_key=abcdef123456&x=1")

	out := buf.String()
	if strings.Contains(out, "hunter2") {
		t.Fatalf("password leaked into the log:\n%s", out)
	}
	if strings.Contains(out, "AAEhBOweik6ad9r_QXbLXyz1234567890abc") {
		t.Fatalf("telegram token leaked into the log:\n%s", out)
	}
	if strings.Contains(out, "abcdef123456") {
		t.Fatalf("api key leaked into the log:\n%s", out)
	}
	if !strings.Contains(out, "a@b.test") {
		t.Fatalf("masking must not swallow non-secret fields:\n%s", out)
	}
	if n := strings.Count(out, Redacted); n < 5 {
		t.Fatalf("expected at least 5 redactions, got %d:\n%s", n, out)
	}
}

func TestLog_LeavesOrdinaryValuesAlone(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, Options{Level: "info", Format: "json", MaskPII: true})
	log.Info("request", "method", "GET", "path", "/api/v1/categories", "status", 200)
	out := buf.String()
	for _, want := range []string{"GET", "/api/v1/categories", "200"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%q missing from %s", want, out)
		}
	}
	if strings.Contains(out, Redacted) {
		t.Fatalf("nothing should be redacted here:\n%s", out)
	}
}

func TestLog_MaskingCanBeDisabled(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, Options{Level: "info", Format: "json", MaskPII: false})
	log.Info("login", "password", "hunter2")
	if !strings.Contains(buf.String(), "hunter2") {
		t.Fatal("with mask_pii=false the raw value is expected; the flag must be honoured")
	}
}

func TestLog_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, Options{Level: "warn", Format: "json", MaskPII: true})
	log.Info("quiet")
	log.Warn("loud")
	out := buf.String()
	if strings.Contains(out, "quiet") || !strings.Contains(out, "loud") {
		t.Fatalf("level filtering wrong: %s", out)
	}
}
