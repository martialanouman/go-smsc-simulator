package schedule

import (
	"reflect"
	"testing"
)

// payloads returns just the Payload of each event, for concise order assertions.
func payloads(evs []Event) []any {
	out := make([]any, len(evs))
	for i, e := range evs {
		out[i] = e.Payload
	}
	return out
}

func TestSchedule_DrainDueReturnsPrefixInTickOrder(t *testing.T) {
	var r Runner
	// Schedule out of tick order; the Runner must keep them sorted by DueTick.
	r.Schedule(15, "b")
	r.Schedule(5, "a")
	r.Schedule(25, "c")

	if got := r.DrainDue(4); got != nil {
		t.Fatalf("nothing due at clock 4, got %v", payloads(got))
	}

	if got, want := payloads(r.DrainDue(15)), []any{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DrainDue(15) = %v, want %v", got, want)
	}
	if got, want := r.Len(), 1; got != want {
		t.Fatalf("Len after partial drain = %d, want %d", got, want)
	}

	if got, want := payloads(r.DrainDue(100)), []any{"c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DrainDue(100) = %v, want %v", got, want)
	}
	if r.Len() != 0 {
		t.Fatalf("Len after full drain = %d, want 0", r.Len())
	}
}

func TestSchedule_SameTickTieBreakByInsertionOrder(t *testing.T) {
	var r Runner
	// Three events all due at the same tick must drain in the order scheduled (Seq).
	r.Schedule(10, "first")
	r.Schedule(10, "second")
	r.Schedule(10, "third")

	got := payloads(r.DrainDue(10))
	want := []any{"first", "second", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("same-tick drain order = %v, want %v", got, want)
	}
}

func TestSchedule_DrainAllReleasesEverythingInOrder(t *testing.T) {
	var r Runner
	r.Schedule(30, "late")
	r.Schedule(10, "early")
	r.Schedule(20, "mid")

	got := payloads(r.DrainAll())
	want := []any{"early", "mid", "late"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DrainAll order = %v, want %v", got, want)
	}
	if r.Len() != 0 {
		t.Fatalf("Len after DrainAll = %d, want 0", r.Len())
	}
	if got := r.DrainAll(); got != nil {
		t.Fatalf("DrainAll on empty = %v, want nil", got)
	}
}

func TestSchedule_SeqStaysMonotonicAcrossFlush(t *testing.T) {
	var r Runner
	r.Schedule(10, "a")
	_ = r.DrainAll() // seq must not reset here
	// A later event scheduled at an earlier tick must still sort correctly and keep a
	// higher Seq than the flushed one, so ordering stays total across the flush boundary.
	r.Schedule(5, "b")
	r.Schedule(5, "c")
	got := payloads(r.DrainAll())
	if want := []any{"b", "c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("post-flush order = %v, want %v", got, want)
	}
}

func TestSchedule_NextDue(t *testing.T) {
	var r Runner
	if _, ok := r.NextDue(); ok {
		t.Fatal("NextDue on empty should report false")
	}
	r.Schedule(40, "x")
	r.Schedule(20, "y")
	if tick, ok := r.NextDue(); !ok || tick != 20 {
		t.Fatalf("NextDue = (%d,%v), want (20,true)", tick, ok)
	}
}

// TestSchedule_Deterministic proves the ordering is a pure function of the schedule
// sequence: two Runners fed the same (dueTick, payload) sequence drain identically.
func TestSchedule_Deterministic(t *testing.T) {
	seq := []struct {
		tick uint64
		p    string
	}{{15, "a"}, {5, "b"}, {15, "c"}, {5, "d"}, {30, "e"}, {5, "f"}}

	drain := func() []any {
		var r Runner
		for _, s := range seq {
			r.Schedule(s.tick, s.p)
		}
		return payloads(r.DrainAll())
	}

	first, second := drain(), drain()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic drain: %v vs %v", first, second)
	}
	// And the order is exactly (DueTick, insertion): the three tick-5 events keep their
	// scheduling order among themselves.
	want := []any{"b", "d", "f", "a", "c", "e"}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("drain order = %v, want %v", first, want)
	}
}
