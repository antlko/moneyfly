package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// ProviderConfig is one configured identity provider, in the shape this package
// needs. The api layer maps config.OIDCProvider onto it, which keeps this
// package independent of the config file's schema.
type ProviderConfig struct {
	ID           string
	Name         string
	Issuer       string
	ClientID     string
	ClientSecret string
	Scopes       []string
}

// Claims is the subset of the ID token this application uses.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
}

// ErrUnknownProvider is returned for a provider id that is not configured.
var ErrUnknownProvider = errors.New("auth: unknown identity provider")

// OIDCRegistry holds the configured providers.
//
// Discovery is deliberately **lazy**: doing it at startup would mean a
// temporarily unreachable identity provider stops the whole instance from
// booting, taking password sign-in down with it. Instead each provider resolves
// on first use and caches the result.
type OIDCRegistry struct {
	baseURL string
	order   []string
	byID    map[string]*provider
}

type provider struct {
	cfg ProviderConfig

	mu       sync.Mutex
	resolved *oidc.Provider
	verifier *oidc.IDTokenVerifier
}

// NewOIDCRegistry builds a registry. baseURL is the externally reachable origin
// and must be non-empty when any provider is configured.
func NewOIDCRegistry(baseURL string, cfgs []ProviderConfig) *OIDCRegistry {
	r := &OIDCRegistry{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		byID:    make(map[string]*provider, len(cfgs)),
	}
	for _, c := range cfgs {
		r.order = append(r.order, c.ID)
		r.byID[c.ID] = &provider{cfg: c}
	}
	return r
}

// List returns the configured providers in config order — id and display name
// only, never credentials.
func (r *OIDCRegistry) List() []ProviderConfig {
	out := make([]ProviderConfig, 0, len(r.order))
	for _, id := range r.order {
		c := r.byID[id].cfg
		out = append(out, ProviderConfig{ID: c.ID, Name: c.Name})
	}
	return out
}

// Enabled reports whether any provider is configured.
func (r *OIDCRegistry) Enabled() bool { return len(r.order) > 0 }

// RedirectURI is the callback URL to register with the provider.
func (r *OIDCRegistry) RedirectURI(id string) string {
	return fmt.Sprintf("%s/api/auth/oidc/%s/callback", r.baseURL, id)
}

// AuthCodeURL builds the URL to send the browser to, with PKCE and a nonce.
func (r *OIDCRegistry) AuthCodeURL(ctx context.Context, id, state, nonce, verifier string) (string, error) {
	p, err := r.resolve(ctx, id)
	if err != nil {
		return "", err
	}
	return p.oauth(r.RedirectURI(id)).AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	), nil
}

// Exchange trades the authorization code for an ID token and returns its claims.
// The nonce must match the one sent with the authorization request, which is
// what stops a token from a different login being replayed here.
func (r *OIDCRegistry) Exchange(ctx context.Context, id, code, nonce, verifier string) (*Claims, error) {
	p, err := r.resolve(ctx, id)
	if err != nil {
		return nil, err
	}

	tok, err := p.oauth(r.RedirectURI(id)).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("auth: code exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return nil, errors.New("auth: provider returned no id_token")
	}

	idToken, err := p.verifier.Verify(ctx, rawID)
	if err != nil {
		return nil, fmt.Errorf("auth: verify id_token: %w", err)
	}
	if idToken.Nonce != nonce {
		return nil, errors.New("auth: id_token nonce mismatch")
	}

	var raw struct {
		Email             string `json:"email"`
		EmailVerified     any    `json:"email_verified"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
	}
	if err := idToken.Claims(&raw); err != nil {
		return nil, fmt.Errorf("auth: decode claims: %w", err)
	}

	name := raw.Name
	if name == "" {
		name = raw.PreferredUsername
	}
	return &Claims{
		Subject:       idToken.Subject,
		Email:         strings.ToLower(strings.TrimSpace(raw.Email)),
		EmailVerified: truthy(raw.EmailVerified),
		Name:          name,
	}, nil
}

// truthy normalises email_verified, which providers emit as either a JSON
// boolean or the strings "true"/"false" — Azure AD and older Keycloak both do
// the latter, and a plain bool decode silently yields false for those.
func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true"
	default:
		return false
	}
}

func (p *provider) oauth(redirectURI string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     p.cfg.ClientID,
		ClientSecret: p.cfg.ClientSecret,
		Endpoint:     p.resolved.Endpoint(),
		RedirectURL:  redirectURI,
		Scopes:       p.cfg.Scopes,
	}
}

func (r *OIDCRegistry) resolve(ctx context.Context, id string) (*provider, error) {
	p, ok := r.byID[id]
	if !ok {
		return nil, ErrUnknownProvider
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.resolved != nil {
		return p, nil
	}

	discovered, err := oidc.NewProvider(ctx, p.cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("auth: discover %s: %w", id, err)
	}
	p.resolved = discovered
	p.verifier = discovered.Verifier(&oidc.Config{ClientID: p.cfg.ClientID})
	return p, nil
}
