package telegram

import (
	"sync"
	"time"

	"github.com/antlko/moneyapp/internal/platform/clock"
)

// attemptLimiter throttles failed /link attempts per chat, so a six-character
// code cannot be brute-forced inside its ten-minute life.
//
// It is in memory on purpose: it guards a short-lived secret, and a restart that
// clears it costs an attacker nothing they did not already have.
type attemptLimiter struct {
	clock clock.Clock

	mu      sync.Mutex
	windows map[int64]*window
}

type window struct {
	count int
	until time.Time
}

func newAttemptLimiter(clk clock.Clock) *attemptLimiter {
	return &attemptLimiter{clock: clk, windows: map[int64]*window{}}
}

// allow reports whether the chat may try again.
func (l *attemptLimiter) allow(chatID int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	w, ok := l.windows[chatID]
	if !ok || now.After(w.until) {
		return true
	}
	return w.count < MaxLinkAttempts
}

// fail records a wrong code.
func (l *attemptLimiter) fail(chatID int64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.clock.Now()
	w, ok := l.windows[chatID]
	if !ok || now.After(w.until) {
		l.windows[chatID] = &window{count: 1, until: now.Add(AttemptWindow)}
		return
	}
	w.count++
}

// reset clears the counter after a successful link.
func (l *attemptLimiter) reset(chatID int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.windows, chatID)
}
