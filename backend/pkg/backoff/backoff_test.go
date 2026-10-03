package backoff

import (
	"testing"
	"time"
)

func TestDynamicBackoff(t *testing.T) {
	b := NewDynamic(500*time.Millisecond, 3*time.Second, 2)

	if got := b.Next(true, false); got != 500*time.Millisecond {
		t.Fatalf("work should reset to min interval, got %s", got)
	}
	first := b.Next(false, false)
	second := b.Next(false, false)
	if first >= second {
		t.Fatalf("interval should grow on empty polls: %s -> %s", first, second)
	}
	for i := 0; i < 20; i++ {
		b.Next(false, false)
	}
	if b.Current() > 3*time.Second {
		t.Fatalf("interval must be capped at max, got %s", b.Current())
	}
	if got := b.Next(false, true); got != 3*time.Second {
		t.Fatalf("busy should jump to max interval, got %s", got)
	}
	b.Reset()
	if b.Current() != 500*time.Millisecond {
		t.Fatalf("reset failed, got %s", b.Current())
	}
}
