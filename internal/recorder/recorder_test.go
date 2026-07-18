package recorder_test

import (
	"sync"
	"testing"

	"github.com/martialanouman/go-smsc-simulator/internal/recorder"
)

func rec(source, dest string) recorder.RecordedPDU {
	return recorder.RecordedPDU{SourceAddr: source, DestAddr: dest}
}

// TestRecorder_RingWraps checks that the buffer retains only the most recent
// capacity records while Index keeps climbing monotonically past the wrap.
func TestRecorder_RingWraps(t *testing.T) {
	t.Parallel()

	r := recorder.New(3)
	for i := 0; i < 5; i++ {
		r.Append(rec("s", "d"))
	}

	got := r.Snapshot(recorder.Filter{})
	if len(got) != 3 {
		t.Fatalf("Len after overflow = %d, want 3", len(got))
	}
	// The three survivors are indices 2,3,4 — the oldest two were overwritten.
	for i, want := range []uint64{2, 3, 4} {
		if got[i].Index != want {
			t.Errorf("survivor[%d].Index = %d, want %d", i, got[i].Index, want)
		}
	}
}

// TestRecorder_Filter exercises the address filters, the Since cursor and Limit.
func TestRecorder_Filter(t *testing.T) {
	t.Parallel()

	r := recorder.New(10)
	r.Append(rec("33600", "33611")) // Index 0
	r.Append(rec("33600", "33622")) // Index 1
	r.Append(rec("33700", "33611")) // Index 2

	if got := r.Snapshot(recorder.Filter{SourceAddr: "33600"}); len(got) != 2 {
		t.Errorf("filter by source: got %d, want 2", len(got))
	}
	if got := r.Snapshot(recorder.Filter{DestAddr: "33611"}); len(got) != 2 {
		t.Errorf("filter by dest: got %d, want 2", len(got))
	}
	if got := r.Snapshot(recorder.Filter{Since: 2}); len(got) != 1 || got[0].Index != 2 {
		t.Errorf("since cursor: got %+v, want single Index 2", got)
	}
	if got := r.Snapshot(recorder.Filter{Limit: 1}); len(got) != 1 || got[0].Index != 0 {
		t.Errorf("limit: got %+v, want single Index 0", got)
	}
}

// TestRecorder_SnapshotDoesNotAlias proves a returned snapshot is decoupled from
// the ring: mutating the caller's copy cannot corrupt stored state.
func TestRecorder_SnapshotDoesNotAlias(t *testing.T) {
	t.Parallel()

	r := recorder.New(2)
	r.Append(recorder.RecordedPDU{SourceAddr: "a", ShortMessage: []byte("hi")})

	snap := r.Snapshot(recorder.Filter{})
	snap[0].ShortMessage[0] = 'X'

	fresh := r.Snapshot(recorder.Filter{})
	if string(fresh[0].ShortMessage) != "hi" {
		t.Fatalf("stored message mutated through snapshot: %q", fresh[0].ShortMessage)
	}
}

// TestRecorder_ConcurrentAppends is a race-detector target: many goroutines append
// while others snapshot. It asserts nothing beyond "no data race, no panic".
func TestRecorder_ConcurrentAppends(t *testing.T) {
	t.Parallel()

	r := recorder.New(64)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Append(rec("s", "d"))
				_ = r.Snapshot(recorder.Filter{Limit: 10})
			}
		}()
	}
	wg.Wait()

	if r.Len() != 64 {
		t.Fatalf("Len = %d, want 64 (saturated)", r.Len())
	}
}
