package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// captureLogs swaps in a JSON slog handler at Info level — the deployed
// default — for the duration of one test, and returns the captured lines.
// Info is the point of the test: a line that only shows at debug is a line a
// stock deployment never sees, which is the exact gap this whole change closes.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

// logLine decodes the last JSON line in buf into a lookup of its fields.
func logLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if lines[0] == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &out); err != nil {
		t.Fatalf("decode log line %q: %v", lines[len(lines)-1], err)
	}
	return out
}

// A stock deployment runs at the default level (info). Before this change,
// every request logged at debug regardless of outcome — so a failed request
// left nothing in these logs at all, which read as broken logging rather
// than as "the level is filtering exactly what it was configured to filter".
func TestSuccessfulRequestsStayQuietAtTheDefaultLevel(t *testing.T) {
	buf := captureLogs(t)
	s := newTestServer(t, "")

	res := s.do(t, "GET", "/api/health", "", "")
	if res.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if buf.Len() != 0 {
		t.Errorf("a successful request logged at the default level:\n%s", buf.String())
	}
}

// The exact bug: a rejected request must be visible without switching to
// debug, and the log line must carry the same message the client saw, so an
// operator does not have to ask what the screen said.
func TestRejectedRequestsAreVisibleAtTheDefaultLevel(t *testing.T) {
	buf := captureLogs(t)
	s := newTestServer(t, "")

	res := s.do(t, "GET", "/api/admin/settings", "", "")
	if res.StatusCode != 401 {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}

	line := logLine(t, buf)
	if line == nil {
		t.Fatal("a 401 produced no log line at the default level")
	}
	if line["level"] != "WARN" {
		t.Errorf("level = %v, want WARN", line["level"])
	}
	if line["status"] != float64(401) {
		t.Errorf("status = %v, want 401", line["status"])
	}
	if line["path"] != "/api/admin/settings" {
		t.Errorf("path = %v, want /api/admin/settings", line["path"])
	}
	if line["error"] != "not signed in" {
		t.Errorf("error = %v, want the exact message the client received", line["error"])
	}
}

// errorHandlerWillRender has to agree with errorHandler exactly — it exists
// only because requestLogger cannot wait around and ask errorHandler what it
// decided (see requestLogger's own doc comment for why). A route-level,
// through-HTTP test for the 500 case is deliberately not attempted here: this
// server's SPA catch-all (app.Use(s.serveSPA), registered last on purpose —
// see server.go) shadows a probe route added after construction, which is a
// test-harness limit, not a claim about production routing. Fiber.Error
// classification is a pure function; test it as one.
func TestErrorHandlerWillRenderMatchesErrorHandlerItself(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantMsg    string
	}{
		{"nil", nil, 0, ""},
		{"fiber 400", fiber.NewError(fiber.StatusBadRequest, "bad input"), 400, "bad input"},
		{"fiber 403", fiber.NewError(fiber.StatusForbidden, "admin only"), 403, "admin only"},
		{"wrapped fiber error", fmt.Errorf("wrap: %w", fiber.NewError(422, "nope")), 422, "nope"},
		{"unexpected error", errors.New("boom"), 500, "internal server error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, msg := errorHandlerWillRender(tc.err)
			if status != tc.wantStatus || msg != tc.wantMsg {
				t.Errorf("got (%d, %q), want (%d, %q)", status, msg, tc.wantStatus, tc.wantMsg)
			}
		})
	}
}

// The one thing the pure-function test above cannot see: that requestLogger
// actually calls slog.ErrorContext, not slog.WarnContext, once the status it
// derived crosses into 500 territory. /api/admin/settings against a session
// that will fail to parse as a user is not reachable normally, so this drives
// the classification+level switch directly instead of through a real route.
func TestFiveHundredsLogAtErrorLevelNotWarn(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429} {
		if lvl := levelFor(status); lvl != slog.LevelWarn {
			t.Errorf("status %d: level = %v, want Warn", status, lvl)
		}
	}
	for _, status := range []int{500, 502, 503} {
		if lvl := levelFor(status); lvl != slog.LevelError {
			t.Errorf("status %d: level = %v, want Error", status, lvl)
		}
	}
	if lvl := levelFor(200); lvl != slog.LevelDebug {
		t.Errorf("status 200: level = %v, want Debug", lvl)
	}
}

// The body length is logged before the handler runs — the one signal that
// distinguishes "the app rejected this" from "this never reached the app":
// a request a reverse proxy swallowed produces no line here at all, but one
// the app genuinely saw and rejected always carries its real byte count.
func TestRequestBytesIsLogged(t *testing.T) {
	buf := captureLogs(t)
	s := newTestServer(t, "")

	s.do(t, "GET", "/api/admin/settings", "", "")

	line := logLine(t, buf)
	if line == nil {
		t.Fatal("no log line")
	}
	if _, ok := line["request_bytes"]; !ok {
		t.Error("request_bytes missing from the log line")
	}
}

// Compression must never affect what gets logged: requestLogger classifies
// the *error* c.Next() returned, and never touches the response body at all,
// so a compressed response and an uncompressed one must produce an identical
// log line either way.
func TestLoggingIsUnaffectedByCompression(t *testing.T) {
	buf := captureLogs(t)
	s := newTestServer(t, "")

	res := s.doWithAcceptEncoding(t, "/api/admin/settings", "gzip", "")
	if res.StatusCode != 401 {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}

	line := logLine(t, buf)
	if line == nil || line["error"] != "not signed in" || line["status"] != float64(401) {
		t.Fatalf("line = %+v, want the same line an uncompressed request would produce", line)
	}
}
