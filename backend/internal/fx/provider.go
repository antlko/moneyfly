// The outbound rate clients: open.er-api.com as primary and
// fawazahmed0/currency-api as fallback, both free and keyless. Which ones run,
// and in what order, is `fx.providers` in config.yaml.
//
// No credentialed provider exists here and none is planned — a self-hosted
// tracker should not need an account somewhere else to show a total.
//
// Nothing in the request path ever waits on a provider: the refresh loop is the
// only caller, and a failure keeps the last stored value rather than breaking a
// render.
package fx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// RateProvider fetches EUR-based rates.
type RateProvider interface {
	Key() string
	// Fetch returns EUR->X rates. A partial response is rejected wholesale: half
	// a rate table would silently freeze the currencies it omitted at yesterday's
	// value while the rest moved.
	Fetch(ctx context.Context, base string) (map[string]*big.Rat, time.Time, error)
}

// Timeout bounds one provider call. Ten seconds is generous for a JSON document
// and short enough that a hung endpoint cannot hold the scheduler.
const Timeout = 10 * time.Second

// Client is the shared HTTP plumbing: one retry, a bounded timeout, and no
// redirects to somewhere unexpected.
type Client struct {
	http *http.Client
}

// NewClient builds an HTTP client for the providers.
func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: Timeout}}
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	// One retry: a single transient failure should not cost a day's rates, and
	// more than one would just delay the fallback.
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("provider: building request for %s: %w", url, err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "moneyfly/1 (+https://github.com/antlko/moneyfly)")

		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("provider: %s returned %d", url, resp.StatusCode)
			continue
		}
		return body, nil
	}
	return nil, fmt.Errorf("provider: fetching %s: %w", url, lastErr)
}

// OpenErAPI is the primary provider: 161 currencies, no key, and it publishes
// `time_next_update_utc`, so the scheduler is told when to come back instead of
// guessing.
type OpenErAPI struct {
	client   *Client
	endpoint string
	required []string
}

// NewOpenErAPI builds the primary provider. `required` is the set of quotes a
// response must carry to be accepted — see requireAll.
func NewOpenErAPI(client *Client, endpoint string, required []string) *OpenErAPI {
	if endpoint == "" {
		endpoint = "https://open.er-api.com/v6/latest/"
	}
	return &OpenErAPI{client: client, endpoint: endpoint, required: required}
}

// Key implements RateProvider.
func (p *OpenErAPI) Key() string { return "open-er-api" }

// Fetch implements RateProvider.
func (p *OpenErAPI) Fetch(ctx context.Context, base string) (map[string]*big.Rat, time.Time, error) {
	body, err := p.client.get(ctx, urlFor(p.endpoint, strings.ToUpper(base)))
	if err != nil {
		return nil, time.Time{}, err
	}
	return ParseOpenErAPI(body, base, p.required)
}

// ParseOpenErAPI decodes a response. It is exported so the tests can drive it
// from a recorded fixture rather than the live endpoint.
func ParseOpenErAPI(body []byte, base string, required []string) (map[string]*big.Rat, time.Time, error) {
	var envelope struct {
		Result            string                 `json:"result"`
		BaseCode          string                 `json:"base_code"`
		TimeLastUpdateUTC string                 `json:"time_last_update_utc"`
		Rates             map[string]json.Number `json:"rates"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&envelope); err != nil {
		return nil, time.Time{}, fmt.Errorf("provider: open-er-api: %w", err)
	}
	if envelope.Result != "" && envelope.Result != "success" {
		return nil, time.Time{}, fmt.Errorf("provider: open-er-api reported %q", envelope.Result)
	}
	if !strings.EqualFold(envelope.BaseCode, base) {
		return nil, time.Time{}, fmt.Errorf(
			"provider: open-er-api returned base %q, want %q", envelope.BaseCode, base)
	}

	rates, err := toRats(envelope.Rates)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("provider: open-er-api: %w", err)
	}
	if err := requireAll(rates, required); err != nil {
		return nil, time.Time{}, fmt.Errorf("provider: open-er-api: %w", err)
	}
	return rates, parseUpdatedAt(envelope.TimeLastUpdateUTC), nil
}

// Fawazahmed0 is the fallback: 338 tickers including XAU and USDT, served from a
// CDN, no key.
type Fawazahmed0 struct {
	client   *Client
	endpoint string
	required []string
}

// NewFawazahmed0 builds the fallback provider.
func NewFawazahmed0(client *Client, endpoint string, required []string) *Fawazahmed0 {
	if endpoint == "" {
		endpoint = "https://cdn.jsdelivr.net/npm/@fawazahmed0/currency-api@latest/v1/currencies/"
	}
	return &Fawazahmed0{client: client, endpoint: endpoint, required: required}
}

// Key implements RateProvider.
func (p *Fawazahmed0) Key() string { return "fawazahmed0" }

// Fetch implements RateProvider.
func (p *Fawazahmed0) Fetch(ctx context.Context, base string) (map[string]*big.Rat, time.Time, error) {
	body, err := p.client.get(ctx, urlFor(p.endpoint, strings.ToLower(base)+".json"))
	if err != nil {
		return nil, time.Time{}, err
	}
	return ParseFawazahmed0(body, base, p.required)
}

// ParseFawazahmed0 decodes a response.
//
// The payload keys the rate table by the lower-cased base code, so the shape is
// `{"date":"...","eur":{"usd":1.13,...}}` and the table has to be found rather
// than named.
func ParseFawazahmed0(body []byte, base string, required []string) (map[string]*big.Rat, time.Time, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, time.Time{}, fmt.Errorf("provider: fawazahmed0: %w", err)
	}

	var date time.Time
	if raw, ok := envelope["date"]; ok {
		var text string
		if err := json.Unmarshal(raw, &text); err == nil {
			if parsed, err := time.Parse("2006-01-02", text); err == nil {
				date = parsed.UTC()
			}
		}
	}

	raw, ok := envelope[strings.ToLower(base)]
	if !ok {
		return nil, time.Time{}, fmt.Errorf(
			"provider: fawazahmed0 has no %q table", strings.ToLower(base))
	}
	var table map[string]json.Number
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&table); err != nil {
		return nil, time.Time{}, fmt.Errorf("provider: fawazahmed0: %w", err)
	}

	rates, err := toRats(table)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("provider: fawazahmed0: %w", err)
	}
	if err := requireAll(rates, required); err != nil {
		return nil, time.Time{}, fmt.Errorf("provider: fawazahmed0: %w", err)
	}
	return rates, date, nil
}

// toRats parses the decimal text exactly. Going through float64 would reintroduce
// the drift the TEXT column exists to avoid.
func toRats(table map[string]json.Number) (map[string]*big.Rat, error) {
	out := make(map[string]*big.Rat, len(table))
	for code, value := range table {
		rate, ok := new(big.Rat).SetString(value.String())
		if !ok {
			return nil, fmt.Errorf("%q is not a decimal rate for %s", value.String(), code)
		}
		if rate.Sign() <= 0 {
			return nil, fmt.Errorf("rate for %s is not positive: %s", code, value.String())
		}
		out[strings.ToUpper(code)] = rate
	}
	return out, nil
}

// requireAll rejects a partial response. Applying half a table would freeze the
// missing currencies at yesterday's value while the rest moved, which is worse
// than fetching nothing — and it would do so invisibly, because the stale value
// still renders.
//
// `required` is the set of currencies this instance actually uses, computed from
// the data rather than hardcoded: adding a HUF account is what makes the
// refresher start insisting on HUF, and a provider that stops publishing it then
// fails over instead of quietly freezing. An empty set requires nothing, which is
// the right answer for an instance with one currency.
func requireAll(rates map[string]*big.Rat, required []string) error {
	missing := []string{}
	for _, code := range required {
		if _, ok := rates[code]; !ok {
			missing = append(missing, code)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("response is missing %s; a partial table is rejected whole",
			strings.Join(missing, ", "))
	}
	return nil
}

// urlFor appends the base-specific suffix unless the configured endpoint already
// carries it. Both forms have to work: the documented endpoints are written with
// the suffix, while the constructors take a prefix.
func urlFor(endpoint, suffix string) string {
	if strings.HasSuffix(endpoint, "/") {
		return endpoint + suffix
	}
	if strings.HasSuffix(strings.ToLower(endpoint), strings.ToLower(suffix)) {
		return endpoint
	}
	return endpoint + "/" + suffix
}

func parseUpdatedAt(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC1123, time.RFC1123Z, "Mon, 02 Jan 2006 15:04:05 -0700"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}
