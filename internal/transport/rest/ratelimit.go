package rest

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/platform/clock"
)

// loginLimiter applies per-IP exponential backoff to failed logins.
//
// Per-account lockout is deliberately absent: locking an account by email is a
// denial-of-service lever against a known address
// (docs/implementation-plan/02-auth-and-taxonomy.md task 11).
type loginLimiter struct {
	mu      sync.Mutex
	attempt map[string]*attemptState
	clock   clock.Clock

	// threshold is the number of failures tolerated before backoff starts.
	threshold int
	// window is how long a quiet period must be before the counter resets.
	window time.Duration
}

type attemptState struct {
	failures int
	last     time.Time
	until    time.Time
}

func newLoginLimiter(threshold int, window time.Duration, clk clock.Clock) *loginLimiter {
	if threshold < 1 {
		threshold = 5
	}
	if window <= 0 {
		window = 15 * time.Minute
	}
	if clk == nil {
		clk = clock.Real()
	}
	return &loginLimiter{attempt: map[string]*attemptState{}, clock: clk, threshold: threshold, window: window}
}

// blocked reports whether the caller must wait, and for how long.
func (l *loginLimiter) blocked(ip string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.attempt[ip]
	if !ok {
		return 0, false
	}
	now := l.clock.Now()
	if now.Sub(st.last) > l.window {
		delete(l.attempt, ip)
		return 0, false
	}
	if now.Before(st.until) {
		return st.until.Sub(now).Round(time.Second) + time.Second, true
	}
	return 0, false
}

// fail records a failed attempt and extends the backoff.
func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	st, ok := l.attempt[ip]
	if !ok || now.Sub(st.last) > l.window {
		st = &attemptState{}
		l.attempt[ip] = st
	}
	st.failures++
	st.last = now
	if st.failures >= l.threshold {
		// 1s, 2s, 4s, ... capped, so a script slows to a crawl while a mistyped
		// password costs a human nothing.
		exp := st.failures - l.threshold
		if exp > 10 {
			exp = 10
		}
		delay := time.Duration(math.Pow(2, float64(exp))) * time.Second
		st.until = now.Add(delay)
	}
	// Keep the map from growing without bound on a scan of many source addresses.
	if len(l.attempt) > 10000 {
		for k, v := range l.attempt {
			if now.Sub(v.last) > l.window {
				delete(l.attempt, k)
			}
		}
	}
}

// reset clears the counter after a successful login.
func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempt, ip)
}

func writeRateLimited(c *fiber.Ctx, retryAfter time.Duration) error {
	seconds := int(retryAfter.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	c.Set(fiber.HeaderRetryAfter, strconv.Itoa(seconds))
	return sendProblem(c, http.StatusTooManyRequests, Problem{
		Type:      ProblemBase + "rate-limited",
		Title:     "Too many requests",
		Status:    http.StatusTooManyRequests,
		Detail:    "Too many failed attempts. Try again in " + strconv.Itoa(seconds) + "s.",
		RequestID: requestID(c),
	})
}
