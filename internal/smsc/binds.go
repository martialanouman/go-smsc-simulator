package smsc

import (
	"sort"
	"sync"
	"time"

	"github.com/martialanouman/go-smsc-simulator/internal/observability"
)

// bindInfo is one active bound session, as tracked for the read-only /binds view.
// connectedAt is wall-clock metadata for the operator; it is never read on a
// decision path (plan §1.5), so using time.Now here does not touch determinism.
type bindInfo struct {
	id          uint64
	systemID    string
	bindType    string
	connectedAt time.Time
}

// bindRegistry tracks the bound sessions of one virtual SMSC. Sessions register on
// a successful bind and deregister on unbind or disconnect; the HTTP surface reads
// the snapshot. It is shared between session goroutines and handlers, hence the
// mutex — separate from the session's lock-free SMPP state (plan §6).
type bindRegistry struct {
	mu    sync.RWMutex
	binds map[uint64]bindInfo
}

func newBindRegistry() *bindRegistry {
	return &bindRegistry{binds: make(map[uint64]bindInfo)}
}

func (r *bindRegistry) add(b bindInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.binds[b.id] = b
}

func (r *bindRegistry) remove(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.binds, id)
}

func (r *bindRegistry) count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.binds)
}

// isOldest reports whether id is the oldest bind still registered — the one with the
// smallest id, since ids are assigned in monotonic accept order (plan §1.5). Used by a
// scope: oldest scheduled disconnect: each bind evaluates this when its own clock reaches
// the disconnect tick, so at most the oldest bind then open cuts itself.
func (r *bindRegistry) isOldest(id uint64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for other := range r.binds {
		if other < id {
			return false
		}
	}
	_, ok := r.binds[id]
	return ok
}

// views returns the active binds as observability DTOs, ordered by id so the
// output is stable across calls (id is the accept order within a virtual SMSC).
func (r *bindRegistry) views() []observability.BindView {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]observability.BindView, 0, len(r.binds))
	for _, b := range r.binds {
		out = append(out, observability.BindView{
			ID:          b.id,
			SystemID:    b.systemID,
			BindType:    b.bindType,
			ConnectedAt: b.connectedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
