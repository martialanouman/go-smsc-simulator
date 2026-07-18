// Package recorder is the bounded, queryable journal of submit_sm PDUs a virtual
// SMSC has received. It exists so a test can assert *what the gateway actually
// sent* — addresses, content, encoding — via the read-only surface (plan §1.7, §6).
//
// Retaining message content is the feature here, not a leak: unlike the gateway,
// the simulator is meant to expose received bodies for inspection. Bind secrets are
// never recorded — only the submit_sm payload.
package recorder

import (
	"bytes"
	"sync"
)

// RecordedPDU is one received submit_sm, flattened to the fields worth asserting
// on. Index is a per-recorder monotonic sequence number (never reused, even after
// the ring wraps) so a reader can page with a stable cursor.
type RecordedPDU struct {
	Index        uint64
	MessageID    string
	SourceAddr   string
	SourceTON    uint8
	SourceNPI    uint8
	DestAddr     string
	DestTON      uint8
	DestNPI      uint8
	DataCoding   uint8
	ShortMessage []byte
	PerBindClock uint64
}

// Filter narrows a Snapshot. Zero-valued fields do not filter: an empty SourceAddr
// matches any source, Since 0 starts from the oldest retained record, and a
// non-positive Limit returns every match.
type Filter struct {
	SourceAddr string
	DestAddr   string
	Since      uint64 // inclusive lower bound on Index; page with lastIndex+1
	Limit      int
}

// Recorder is a fixed-capacity ring buffer of RecordedPDU. It is the one piece of
// per-virtual-SMSC state shared between the session goroutines (which Append) and
// the HTTP handlers (which Snapshot), so it carries its own RWMutex — the session's
// SMPP window stays lock-free and single-goroutine-owned (plan §6, CLAUDE.md).
type Recorder struct {
	mu    sync.RWMutex
	ring  []RecordedPDU
	capN  uint64
	total uint64 // count ever appended; also the next Index to assign
	size  int    // number currently retained (int, so Len needs no uint64→int cast)
}

// New returns a Recorder retaining the most recent capacity PDUs. capacity comes
// from pdu_buffer_size, validated positive at config load; New clamps to at least 1
// as a defensive floor so the ring arithmetic can never divide by zero.
func New(capacity int) *Recorder {
	if capacity < 1 {
		capacity = 1
	}
	return &Recorder{ring: make([]RecordedPDU, capacity), capN: uint64(capacity)}
}

// Append records p, assigning it the next Index. When the ring is full the oldest
// record is overwritten; its Index is never handed out again.
func (r *Recorder) Append(p RecordedPDU) {
	r.mu.Lock()
	defer r.mu.Unlock()

	p.Index = r.total
	r.ring[r.total%r.capN] = p
	r.total++
	if r.size < len(r.ring) {
		r.size++
	}
}

// Snapshot returns the retained records matching f, oldest first. The result is a
// fresh slice the caller owns; it never aliases the ring, so a concurrent Append
// cannot mutate a snapshot already handed to an HTTP response.
func (r *Recorder) Snapshot(f Filter) []RecordedPDU {
	r.mu.RLock()
	defer r.mu.RUnlock()

	oldest := uint64(0)
	if r.total > r.capN {
		oldest = r.total - r.capN
	}
	start := max(oldest, f.Since)

	var out []RecordedPDU
	for i := start; i < r.total; i++ {
		rec := r.ring[i%r.capN]
		if f.SourceAddr != "" && rec.SourceAddr != f.SourceAddr {
			continue
		}
		if f.DestAddr != "" && rec.DestAddr != f.DestAddr {
			continue
		}
		// Clone the payload so the returned record is fully owned: a caller must not
		// be able to reach back through ShortMessage and mutate a still-retained PDU.
		rec.ShortMessage = bytes.Clone(rec.ShortMessage)
		out = append(out, rec)
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
	}
	return out
}

// Len reports how many records are currently retained (at most the capacity).
func (r *Recorder) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}
