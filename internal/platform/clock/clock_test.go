package clock

import (
	"testing"
	"time"
)

func TestReal_IsUTC(t *testing.T) {
	if loc := Real().Now().Location(); loc != time.UTC {
		t.Fatalf("Real().Now() must be UTC, got %v", loc)
	}
}

func TestFixed_DoesNotMove(t *testing.T) {
	at := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	c := Fixed(at)
	first, second := c.Now(), c.Now()
	if !first.Equal(at) || !second.Equal(first) {
		t.Fatalf("Fixed must return the same instant every call, got %v then %v", first, second)
	}
}

func TestSteppable_Advances(t *testing.T) {
	at := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	c := NewSteppable(at)
	c.Advance(48 * time.Hour)
	if got := c.Now(); !got.Equal(at.Add(48 * time.Hour)) {
		t.Fatalf("Advance: got %v", got)
	}
}
