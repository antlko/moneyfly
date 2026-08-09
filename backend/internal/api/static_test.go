package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// doWithAcceptEncoding issues a request advertising gzip support. The shared
// helpers deliberately send no Accept-Encoding, so compression tests need their
// own way in rather than a fourth optional parameter on `do`.
func (s *Server) doWithAcceptEncoding(t *testing.T, path, encoding, cookie string) *http.Response {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Accept-Encoding", encoding)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	res, err := s.App().Test(req, fiber.TestConfig{Timeout: testTimeout, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return res
}

// The SPA's critical path is a few hundred KB of JS and CSS on every cold load,
// and Fiber does not compress by default — this is the single largest thing the
// server does for startup time.
func TestSPAIsCompressedWhenTheClientAcceptsIt(t *testing.T) {
	s := newTestServer(t, "")

	res := s.doWithAcceptEncoding(t, "/", "gzip", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want %q", got, "gzip")
	}
}

// A client that does not ask for compression must still get a readable body —
// the middleware has to negotiate, not assume.
func TestSPAIsPlainWhenTheClientDoesNotAcceptCompression(t *testing.T) {
	s := newTestServer(t, "")

	res := s.do(t, "GET", "/", "", "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "<html") {
		t.Errorf("body is not the SPA shell:\n%s", body)
	}
}

// index.html is `no-cache` — it names the hashed asset chunks, so a stale one
// pins the app to an old build and it genuinely must be checked every launch.
// Checking should cost a 304, not the whole file.
func TestIndexRevalidatesWithAnETag(t *testing.T) {
	s := newTestServer(t, "")

	first := s.do(t, "GET", "/", "", "")
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the app shell; every launch re-downloads it in full")
	}
	if got := first.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}

	second := s.doWithHeader(t, "GET", "/", "", "If-None-Match", etag)
	if second.StatusCode != http.StatusNotModified {
		t.Fatalf("status = %d with a matching If-None-Match, want 304", second.StatusCode)
	}
	body, err := io.ReadAll(second.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("304 carried %d bytes, want an empty body", len(body))
	}
}

// A stale tag — the shape a client has after a deploy — must get the new file.
func TestIndexSendsTheShellWhenTheETagDoesNotMatch(t *testing.T) {
	s := newTestServer(t, "")

	res := s.doWithHeader(t, "GET", "/", "", "If-None-Match", `"an-older-build"`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d for a stale ETag, want 200", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "<html") {
		t.Errorf("body is not the app shell:\n%s", body)
	}
}

// A client-side route resolves to the shell too, and must revalidate the same
// way rather than re-downloading on every deep link.
func TestClientRoutesCarryTheSameETag(t *testing.T) {
	s := newTestServer(t, "")

	root := s.do(t, "GET", "/", "", "")
	deep := s.do(t, "GET", "/accounts", "", "")
	if deep.StatusCode != http.StatusOK {
		t.Fatalf("status = %d for a client route, want 200", deep.StatusCode)
	}
	if root.Header.Get("ETag") != deep.Header.Get("ETag") {
		t.Errorf("ETag differs between / (%q) and /accounts (%q); it is the same document",
			root.Header.Get("ETag"), deep.Header.Get("ETag"))
	}
}

// The one route that must never be compressed.
//
// SSE is a response that never ends, delivered a few bytes at a time; a
// compressor buffers those bytes and the stream stops being a stream. The
// failure is invisible from the server's side — every request still returns
// 200 — and shows up only as devices that look offline while syncing fine.
//
// Asserted on the predicate rather than on a live request, because an
// authenticated event stream never completes and the harness would simply time
// out. TestEventStreamSkipMatchesTheRegisteredRoute below is the other half:
// this proves the rule, that proves the rule is pointed at the real route.
func TestEventStreamIsNeverCompressed(t *testing.T) {
	if !skipCompression(eventStreamPath) {
		t.Errorf("skipCompression(%q) = false, want true — compressing SSE "+
			"buffers it and devices then look offline", eventStreamPath)
	}
}

// Everything else should be compressed; the skip is a scalpel, not a blanket.
func TestOnlyTheEventStreamSkipsCompression(t *testing.T) {
	for _, path := range []string{
		"/",
		"/index.html",
		"/assets/index-abc123.js",
		"/api/sync/pull",
		"/api/sync/push",
		"/api/sync/snapshot",
		"/api/auth/me",
		// Near misses, in case the rule is ever loosened to a prefix match.
		"/api/sync/events/",
		"/api/sync/eventsource",
		"/api/sync",
	} {
		if skipCompression(path) {
			t.Errorf("skipCompression(%q) = true, want false", path)
		}
	}
}

// The skip is matched on the path, so it has to be the *same* path the route is
// registered on. Guards the two constants drifting apart.
func TestEventStreamSkipMatchesTheRegisteredRoute(t *testing.T) {
	s := newTestServer(t, "")

	// Unauthenticated is fine: this asserts the route exists at that exact path,
	// which a 401 (rather than the SPA catch-all's 200) already proves.
	res := s.do(t, "GET", eventStreamPath, "", "")
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET %s: status = %d, want 401 — the compression skip is keyed "+
			"on this path, so it must be where the stream actually lives",
			eventStreamPath, res.StatusCode)
	}
}
