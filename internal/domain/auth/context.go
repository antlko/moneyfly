package auth

import "context"

type ctxKey int

const (
	ctxKeyUser ctxKey = iota
	ctxKeyToken
	ctxKeyUserAgent
)

// WithUser attaches the authenticated user. The auth middleware is the only
// place that may call it: user_id comes from the session and nowhere else
// (docs/implementation-plan/00-conventions.md §6).
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, ctxKeyUser, u)
}

// UserFromContext returns the authenticated user, or nil.
func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(ctxKeyUser).(*User)
	return u
}

// WithToken attaches the raw session token, so ChangePassword can keep the
// current session alive while revoking the others.
func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, ctxKeyToken, token)
}

// TokenFromContext returns the raw session token, or "".
func TokenFromContext(ctx context.Context) string {
	t, _ := ctx.Value(ctxKeyToken).(string)
	return t
}

// WithUserAgent records the client's user agent for the session list.
func WithUserAgent(ctx context.Context, ua string) context.Context {
	return context.WithValue(ctx, ctxKeyUserAgent, ua)
}

func userAgentFrom(ctx context.Context) string {
	ua, _ := ctx.Value(ctxKeyUserAgent).(string)
	if len(ua) > 256 {
		return ua[:256]
	}
	return ua
}
