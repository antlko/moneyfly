// Package clock supplies the time source. It is injected from cmd/moneyapp so
// that time-dependent behaviour (session expiry, FX dates, import batches) is
// deterministic under test.
package clock

import "time"

// Clock is the only way code above platform reads the wall clock.
type Clock interface{ Now() time.Time }

type realClock struct{}

// Real returns a Clock backed by time.Now, in UTC. Storage is UTC everywhere
// (docs/02-architecture.md §2.9), so the conversion happens once, here.
func Real() Clock { return realClock{} }

// Now implements Clock.
func (realClock) Now() time.Time { return time.Now().UTC() }

type fixedClock struct{ at time.Time }

// Fixed returns a Clock frozen at t.
func Fixed(t time.Time) Clock { return fixedClock{at: t.UTC()} }

// Now implements Clock.
func (f fixedClock) Now() time.Time { return f.at }

// Steppable is a test clock that can be advanced.
type Steppable struct{ at time.Time }

// NewSteppable returns a clock starting at t that callers advance explicitly.
func NewSteppable(t time.Time) *Steppable { return &Steppable{at: t.UTC()} }

// Now implements Clock.
func (s *Steppable) Now() time.Time { return s.at }

// Advance moves the clock forward.
func (s *Steppable) Advance(d time.Duration) { s.at = s.at.Add(d) }
