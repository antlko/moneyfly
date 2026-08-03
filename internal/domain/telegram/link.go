// Package telegram owns the link between a Telegram chat and an account.
//
// It holds no import logic. The bot relays bytes to the stage-04 importer and
// returns one reply; a second import path here is exactly how the old system
// drifted (docs/adr/0013-telegram-in-process.md).
package telegram

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/clock"
)

// CodeTTL is how long a link code is valid. Ten minutes is long enough to switch
// apps and short enough that a leaked screenshot is worthless by the time it is
// seen.
const CodeTTL = 10 * time.Minute

// CodeLength is the number of characters in a link code.
const CodeLength = 6

// codeAlphabet omits the characters that are read wrongly off a screen: 0/O and
// 1/I/L. A code is typed by hand, once, on a phone.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// MaxLinkAttempts is how many failed /link attempts one chat may make in the
// window before it is throttled. It is what stops a code being guessed.
const (
	MaxLinkAttempts = 5
	AttemptWindow   = 10 * time.Minute
)

// Link is a code, and after binding, a chat.
type Link struct {
	ID        int64
	UserID    int64
	ChatID    *int64
	Code      string
	ExpiresAt time.Time
	LinkedAt  *time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

// Live reports whether the link is bound and still valid.
func (l Link) Live() bool { return l.LinkedAt != nil && l.RevokedAt == nil }

// Repo is the storage contract.
type Repo interface {
	Create(ctx context.Context, l Link, now time.Time) (Link, error)
	ByCode(ctx context.Context, code string) (Link, error)
	// LiveByChat resolves a chat to its account. apperr.ErrNotFound means the
	// chat is not linked, which is an ordinary answer rather than a failure.
	LiveByChat(ctx context.Context, chatID int64) (Link, error)
	ListByUser(ctx context.Context, userID int64) ([]Link, error)
	Bind(ctx context.Context, id, chatID int64, now time.Time) (Link, error)
	Revoke(ctx context.Context, userID, id int64, now time.Time) error
	RevokeByChat(ctx context.Context, chatID int64, now time.Time) (int, error)
}

// OffsetStore persists the poll offset across restarts.
type OffsetStore interface {
	Offset(ctx context.Context) (int64, error)
	SetOffset(ctx context.Context, offset int64, now time.Time) error
}

// Service mints and resolves links.
type Service struct {
	repo  Repo
	clock clock.Clock

	// attempts throttles /link per chat. It is in memory because it protects a
	// ten-minute code, and a restart that clears it costs nothing.
	attempts *attemptLimiter
}

// NewService builds the link service.
func NewService(repo Repo, clk clock.Clock) *Service {
	return &Service{repo: repo, clock: clk, attempts: newAttemptLimiter(clk)}
}

// Mint issues a single-use code for a user to type into the chat.
func (s *Service) Mint(ctx context.Context, userID int64) (Link, error) {
	code, err := newCode()
	if err != nil {
		return Link{}, err
	}
	now := s.clock.Now()
	return s.repo.Create(ctx, Link{
		UserID: userID, Code: code, ExpiresAt: now.Add(CodeTTL),
	}, now)
}

// Redeem binds a chat to the account that minted the code.
//
// Every failure returns the same message shape, so a wrong code cannot be told
// apart from an expired or already-used one by anyone guessing.
func (s *Service) Redeem(ctx context.Context, chatID int64, code string) (Link, error) {
	if !s.attempts.allow(chatID) {
		return Link{}, apperr.Forbiddenf("too many attempts from chat %d", chatID)
	}
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return Link{}, apperr.Validation("code", "a code is required")
	}

	link, err := s.repo.ByCode(ctx, code)
	if err != nil {
		s.attempts.fail(chatID)
		return Link{}, apperr.Validation("code", "that code is not valid")
	}
	now := s.clock.Now()
	switch {
	case link.LinkedAt != nil:
		s.attempts.fail(chatID)
		return Link{}, apperr.Validation("code", "that code is not valid")
	case now.After(link.ExpiresAt):
		s.attempts.fail(chatID)
		return Link{}, apperr.Validation("code", "that code is not valid")
	}

	// One live link per chat: re-linking replaces, it does not accumulate.
	if _, err := s.repo.RevokeByChat(ctx, chatID, now); err != nil {
		return Link{}, err
	}
	bound, err := s.repo.Bind(ctx, link.ID, chatID, now)
	if err != nil {
		return Link{}, err
	}
	s.attempts.reset(chatID)
	return bound, nil
}

// Resolve returns the account a chat speaks for.
func (s *Service) Resolve(ctx context.Context, chatID int64) (Link, error) {
	return s.repo.LiveByChat(ctx, chatID)
}

// List returns a user's links, for the settings screen.
func (s *Service) List(ctx context.Context, userID int64) ([]Link, error) {
	return s.repo.ListByUser(ctx, userID)
}

// Revoke ends a link from the web UI.
func (s *Service) Revoke(ctx context.Context, userID, id int64) error {
	return s.repo.Revoke(ctx, userID, id, s.clock.Now())
}

// RevokeChat ends a link from the chat itself, via /unlink.
func (s *Service) RevokeChat(ctx context.Context, chatID int64) (int, error) {
	return s.repo.RevokeByChat(ctx, chatID, s.clock.Now())
}

// newCode returns a cryptographically random code. math/rand would make codes
// predictable from one observed value.
func newCode() (string, error) {
	buf := make([]byte, CodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("telegram: generating a link code: %w", err)
	}
	out := make([]byte, CodeLength)
	for i, b := range buf {
		out[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}
	return string(out), nil
}
