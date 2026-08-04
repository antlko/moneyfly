package fx

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Provider tests run against recorded fixtures, not live endpoints: CI must not
// depend on someone else's uptime.

// required is the stand-in for "the currencies this instance uses". The fixtures
// were recorded for an instance holding euro, forint and hryvnia.
var required = []string{"USD", "HUF", "UAH"}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	return body
}

func TestProvider_ParseOpenErApi(t *testing.T) {
	rates, asOf, err := ParseOpenErAPI(fixture(t, "open-er-api-eur.json"), "EUR", required)
	if err != nil {
		t.Fatalf("ParseOpenErAPI: %v", err)
	}

	want := map[string]string{
		"USD": "1.138108", "HUF": "360.409427", "UAH": "51.080258",
	}
	for code, decimal := range want {
		expected, _ := new(big.Rat).SetString(decimal)
		if rates[code] == nil {
			t.Fatalf("no rate for %s", code)
		}
		if rates[code].Cmp(expected) != 0 {
			t.Errorf("%s = %s, want %s exactly — the decimal text must not go through a float",
				code, rates[code].FloatString(12), decimal)
		}
	}
	if asOf.IsZero() {
		t.Error("the response publishes time_last_update_utc; it must be read")
	}
	if asOf.Year() != 2026 || asOf.Month() != time.July {
		t.Errorf("as-of = %s, want 2026-07-29", asOf.Format(time.RFC3339))
	}
}

func TestProvider_ParseFawazahmed(t *testing.T) {
	rates, asOf, err := ParseFawazahmed0(fixture(t, "fawazahmed0-eur.json"), "EUR", required)
	if err != nil {
		t.Fatalf("ParseFawazahmed0: %v", err)
	}

	expected, _ := new(big.Rat).SetString("51.24129442")
	if rates["UAH"] == nil || rates["UAH"].Cmp(expected) != 0 {
		t.Errorf("UAH = %v, want 51.24129442", rates["UAH"])
	}
	// The fallback is the one that carries the metal and crypto tickers.
	if rates["XAU"] == nil {
		t.Error("XAU is missing; it is why this provider is the price source")
	}
	if rates["USDT"] == nil {
		t.Error("USDT is missing")
	}
	if asOf.Format("2006-01-02") != "2026-07-29" {
		t.Errorf("as-of = %s, want 2026-07-29", asOf.Format("2006-01-02"))
	}
}

func TestProvider_PartialResponseRejected(t *testing.T) {
	_, _, err := ParseOpenErAPI(fixture(t, "open-er-api-partial.json"), "EUR", required)
	if err == nil {
		t.Fatal("a response missing HUF and UAH must be rejected whole")
	}
	// Half a table would freeze the missing currencies at yesterday's value while
	// the rest moved, which is worse than fetching nothing.
	if !strings.Contains(err.Error(), "HUF") || !strings.Contains(err.Error(), "UAH") {
		t.Errorf("error = %q, want it to name the missing currencies", err)
	}
}

func TestProvider_MalformedRejected(t *testing.T) {
	cases := map[string][]byte{
		"not json":       []byte("<html>502 Bad Gateway</html>"),
		"wrong base":     []byte(`{"result":"success","base_code":"USD","rates":{"EUR":1}}`),
		"failure result": []byte(`{"result":"error","base_code":"EUR","rates":{"USD":1,"HUF":1,"UAH":1}}`),
		"rate not a number": []byte(
			`{"result":"success","base_code":"EUR","rates":{"USD":"soon","HUF":1,"UAH":1}}`),
		"negative rate": []byte(
			`{"result":"success","base_code":"EUR","rates":{"USD":-1,"HUF":1,"UAH":1}}`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ParseOpenErAPI(body, "EUR", required); err == nil {
				t.Fatal("must be rejected, with nothing partially applied")
			}
		})
	}
}

func TestProvider_FetchUsesTheConfiguredEndpoint(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "open-er-api-eur.json"))
	}))
	defer server.Close()

	p := NewOpenErAPI(NewClient(), server.URL+"/v6/latest/", required)
	rates, _, err := p.Fetch(context.Background(), "EUR")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(rates) == 0 {
		t.Fatal("no rates returned")
	}
	if path != "/v6/latest/EUR" {
		t.Errorf("requested %q, want /v6/latest/EUR", path)
	}
}

// TestProvider_FetchRetriesOnce checks the single retry: one transient failure
// should not cost a day's rates, and more than one would only delay the fallback.
func TestProvider_FetchRetriesOnce(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write(fixture(t, "fawazahmed0-eur.json"))
	}))
	defer server.Close()

	p := NewFawazahmed0(NewClient(), server.URL+"/v1/currencies/", required)
	if _, _, err := p.Fetch(context.Background(), "EUR"); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestProvider_FetchGivesUpAfterTheRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	p := NewOpenErAPI(NewClient(), server.URL+"/", required)
	if _, _, err := p.Fetch(context.Background(), "EUR"); err == nil {
		t.Fatal("a persistently failing endpoint must return an error, not hang")
	}
}

func TestProvider_URLForToleratesBothEndpointForms(t *testing.T) {
	// The documented endpoints carry the suffix; the constructors take a prefix.
	// Both have to work.
	cases := []struct {
		endpoint, suffix, want string
	}{
		{"https://open.er-api.com/v6/latest/", "EUR", "https://open.er-api.com/v6/latest/EUR"},
		{"https://open.er-api.com/v6/latest/EUR", "EUR", "https://open.er-api.com/v6/latest/EUR"},
		{"https://cdn.example/v1/currencies/", "eur.json", "https://cdn.example/v1/currencies/eur.json"},
		{"https://cdn.example/v1/currencies/eur.json", "eur.json", "https://cdn.example/v1/currencies/eur.json"},
	}
	for _, c := range cases {
		if got := urlFor(c.endpoint, c.suffix); got != c.want {
			t.Errorf("urlFor(%q, %q) = %q, want %q", c.endpoint, c.suffix, got, c.want)
		}
	}
}
