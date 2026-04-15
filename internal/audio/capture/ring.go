package capture

import "sync"

// Ring is a bounded int16 buffer with drop-oldest overwrite semantics, intended
// for handing mic samples from the malgo audio thread to the Go consumer
// goroutine. Single-producer single-consumer assumptions hold in practice, but
// the API is guarded with a mutex so stray races are safe.
type Ring struct {
	mu       sync.Mutex
	buf      []int16
	head     int // next write position
	size     int // number of valid samples
	capacity int
	dropped  int64
}

func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{
		buf:      make([]int16, capacity),
		capacity: capacity,
	}
}

// Push writes samples, overwriting the oldest on overflow. Returns the number
// of samples that were dropped by this call.
func (r *Ring) Push(samples []int16) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	dropped := 0
	for _, s := range samples {
		r.buf[r.head] = s
		r.head = (r.head + 1) % r.capacity
		if r.size < r.capacity {
			r.size++
		} else {
			dropped++
		}
	}
	r.dropped += int64(dropped)
	return dropped
}

// PopAll returns every sample currently in the ring, oldest-first, and clears
// the ring.
func (r *Ring) PopAll() []int16 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size == 0 {
		return nil
	}
	out := make([]int16, r.size)
	start := (r.head - r.size + r.capacity) % r.capacity
	for i := 0; i < r.size; i++ {
		out[i] = r.buf[(start+i)%r.capacity]
	}
	r.size = 0
	return out
}

// Dropped returns the cumulative count of overwritten samples since creation.
func (r *Ring) Dropped() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.dropped
}
