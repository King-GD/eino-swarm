package swarm

import (
	"fmt"
	"sync"
)

// defaultMaxConcurrent is what a library caller gets when they leave
// MaxConcurrent unset. The app config default is separate (6) and is
// written onto the registry before the first spawn.
const defaultMaxConcurrent = 8

// slotGate is a resizable counting semaphore.
//
// A buffered channel plus sync.Once cannot grow: Settings raising
// max_concurrent used to leave queued workers blocked until a restart,
// because the first spawn minted the only channel the registry would
// ever have. Waiters sit on cond; resize wakes them when the new cap is
// above the number of holders. Holders are the workers actually inside a
// model call, oldest first. A lower cap stops the newest holders until
// that number is back within the cap; their slots stay occupied until
// those workers leave, so a queued waiter cannot take a freed slot early.
type slotGate struct {
	mu      sync.Mutex
	cond    *sync.Cond
	cap     int
	held    int
	holders []*Handle
}

func newSlotGate(n int) *slotGate {
	if n <= 0 {
		n = defaultMaxConcurrent
	}
	g := &slotGate{cap: n}
	g.cond = sync.NewCond(&g.mu)
	return g
}

func (g *slotGate) acquire(h *Handle) {
	g.mu.Lock()
	for g.held >= g.cap {
		g.cond.Wait()
	}
	g.held++
	g.holders = append(g.holders, h)
	g.mu.Unlock()
}

func (g *slotGate) release(id string) {
	g.mu.Lock()
	next := make([]*Handle, 0, len(g.holders))
	for _, h := range g.holders {
		if h.ID != id {
			next = append(next, h)
		}
	}
	g.holders = next
	if g.held > 0 {
		g.held--
	}
	if g.held < g.cap {
		g.cond.Signal()
	}
	g.mu.Unlock()
}

// resize returns the newest holders that no longer fit. The caller stops
// them after releasing this lock. held stays until those workers release,
// so waiters remain blocked while the task is still at the new cap.
func (g *slotGate) resize(n int) []*Handle {
	if n <= 0 {
		n = defaultMaxConcurrent
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cap = n
	var extra []*Handle
	for len(g.holders) > g.cap {
		last := len(g.holders) - 1
		extra = append(extra, g.holders[last])
		g.holders = g.holders[:last]
	}
	if g.held < g.cap {
		g.cond.Broadcast()
	}
	return extra
}

// atCapacityError is what spawn_agent and resume_agent return when another
// live worker would pass MaxConcurrent. The handle is not created, so the
// roster does not grow a row that only sits on the gate. Registry.Spawn and
// Registry.Resume still queue: a host that spawns in code can park work, and
// raising the cap still wakes those waiters.
type atCapacityError struct{ n int }

func (e *atCapacityError) Error() string {
	return fmt.Sprintf("swarm: concurrency cap %d reached", e.n)
}

func (r *Registry) capLocked() int {
	return r.advertisedCap()
}

// advertisedCap is the number the manager is told and the tools enforce.
// A non-positive setting is the library default, same as the gate.
func (r *Registry) advertisedCap() int {
	n := r.MaxConcurrent
	if n <= 0 {
		n = defaultMaxConcurrent
	}
	return n
}

// liveCountLocked is workers whose current life has not finished, including
// any still blocked on the gate. Caller holds r.mu. A finished handle still
// in the map does not count: resuming it is what frees a slot for the next piece.
func (r *Registry) liveCountLocked() int {
	n := 0
	for _, h := range r.agents {
		if _, _, done := h.Result(); !done {
			n++
		}
	}
	return n
}
