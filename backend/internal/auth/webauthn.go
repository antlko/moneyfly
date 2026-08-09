package auth

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// NewWebAuthn builds the passkey relying-party client from the instance's
// externally reachable origin — config.Server.BaseURL, the same value OIDC's
// redirect URI already trusts.
//
// The relying-party id and origin are resolved **once, here**, and never from
// an incoming request. That is the whole security property: RPOrigins is an
// allowlist the library checks the browser-asserted origin against, so deriving
// it from whatever Host a request happened to carry — the way requestIsHTTPS
// derives scheme from X-Forwarded-Proto — would make the check compare a value
// against itself. It also matters for correctness beyond that: a credential is
// permanently bound to the rp id it was created under, so an id that varies by
// request would strand every passkey the moment it changed.
//
// A baseURL that will not parse is an error, not a panic or a silent default:
// the caller treats it as "passkeys stay off" and logs it, leaving password and
// OIDC sign-in working — the same shape as this package's lazy OIDC discovery,
// where one unreachable provider must not stop the instance from booting.
func NewWebAuthn(baseURL, rpDisplayName string) (*webauthn.WebAuthn, error) {
	origin := strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("auth: server.base_url %q is not a valid absolute URL", baseURL)
	}

	return webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: rpDisplayName,
		RPOrigins:     []string{origin},
		// No attestation. Attestation exists to prove *which model* of
		// authenticator was used, which matters to an enterprise enrolling only
		// vetted hardware and not at all to a self-hosted personal ledger —
		// asking for it would mean carrying a FIDO metadata service to check it
		// against, to answer a question nothing here asks.
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			// Preferred, not Required. Sign-in here is usernameless, which only
			// works with a discoverable ("resident") credential, so every new
			// passkey should be one — but Required hard-fails registration on an
			// authenticator that cannot store them, and this UI offers no
			// second, email-first path to fall back to. Every platform
			// authenticator people actually use does this unprompted anyway.
			ResidentKey: protocol.ResidentKeyRequirementPreferred,
			// Preferred rather than Required for the same reason: it asks for
			// the biometric/PIN check that makes a passkey a second factor in
			// its own right, without refusing an authenticator that cannot.
			UserVerification: protocol.VerificationPreferred,
		},
	})
}
