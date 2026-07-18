package fault

import (
	"testing"
	"time"
)

func TestWallWindow_CapWithinWindow(t *testing.T) {
	t.Parallel()

	base := time.Unix(1_700_000_000, 0)
	clock := base
	g := NewWallWindow(3)
	g.now = func() time.Time { return clock }

	// First cap submits within the window are allowed, the rest throttled.
	for i := 1; i <= 3; i++ {
		if !g.Allow() {
			t.Fatalf("submit %d within cap must be allowed", i)
		}
	}
	if g.Allow() {
		t.Fatalf("submit beyond cap must be throttled")
	}
	if g.Allow() {
		t.Fatalf("submit still beyond cap must stay throttled")
	}

	// A new one-second window resets the count.
	clock = base.Add(time.Second)
	if !g.Allow() {
		t.Fatalf("first submit of a fresh window must be allowed")
	}
}

func TestWallWindow_SubSecondStaysInWindow(t *testing.T) {
	t.Parallel()

	base := time.Unix(1_700_000_000, 0)
	clock := base
	g := NewWallWindow(2)
	g.now = func() time.Time { return clock }

	if !g.Allow() {
		t.Fatalf("first submit must be allowed")
	}
	if !g.Allow() {
		t.Fatalf("second submit must be allowed")
	}
	clock = base.Add(500 * time.Millisecond) // same window
	if g.Allow() {
		t.Fatalf("third submit in the same sub-second window must be throttled")
	}
}
