package api

import (
	"sync"
	"time"
)

// loginLimiter throttles failed sign-in attempts per key (email or client IP).
//
// It is in-memory and per-process on purpose: moneyfly is a single binary, so
// there is nothing to share state with, and a limiter that needs the database
// would write on every failed guess.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*attempt
}

type attempt struct {
	count int
	until time.Time
}

const (
	maxFailedLogins = 8
	lockoutWindow   = 15 * time.Minute
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: make(map[string]*attempt)}
}

// blocked reports whether the key is currently locked out.
func (l *loginLimiter) blocked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.attempts[key]
	if !ok {
		return false
	}
	if time.Now().After(a.until) {
		delete(l.attempts, key)
		return false
	}
	return a.count >= maxFailedLogins
}

// fail records a failed attempt and extends the window.
func (l *loginLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	a, ok := l.attempts[key]
	if !ok || time.Now().After(a.until) {
		a = &attempt{}
		l.attempts[key] = a
	}
	a.count++
	a.until = time.Now().Add(lockoutWindow)
}

// succeed clears the counter after a correct password.
func (l *loginLimiter) succeed(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
