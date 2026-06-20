// Package dedup provides a small, single-goroutine FIFO deduplicator
// keyed by string id with TTL-based expiry. It is shared by the
// feishu and wechat messaging packages, which used to carry identical
// copies of this code.
//
// Concurrency model:
//
//   - All state lives inside one goroutine (run). External callers
//     send a dedupReq over the inbound channel and block on the
//     response channel until the goroutine answers.
//   - The check channel is unbuffered, so the goroutine back-pressures
//     naturally when it falls behind.
//   - If the goroutine is stuck for >1s, check() assumes the id is new
//     and returns true (fail-open: we'd rather process a duplicate than
//     drop a real message).
package dedup

import (
	"context"
	"time"
)

type request struct {
	id   string
	resp chan bool
}

// Dedup is a TTL-based deduplicator. Construct one per process via
// New; the background goroutine exits when ctx is cancelled.
type Dedup struct {
	in chan request
}

// New starts the background goroutine. capacity caps the number of
// remembered ids; ttl is how long an id stays remembered.
func New(ctx context.Context, capacity int, ttl time.Duration) *Dedup {
	d := &Dedup{in: make(chan request)}
	go d.run(ctx, capacity, ttl)
	return d
}

// Check returns true if id is fresh (caller should process it) and
// false if it has already been seen within the TTL window. On a
// timeout (>1s waiting for the worker), it returns true to avoid
// blocking the caller indefinitely.
func (d *Dedup) Check(id string) bool {
	resp := make(chan bool, 1)
	select {
	case d.in <- request{id: id, resp: resp}:
		return <-resp
	case <-time.After(time.Second):
		return true
	}
}

func (d *Dedup) run(ctx context.Context, capacity int, ttl time.Duration) {
	expires := make(map[string]time.Time, capacity)
	order := make([]string, 0, capacity)
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-d.in:
			now := time.Now()
			for len(order) > 0 {
				if exp, ok := expires[order[0]]; ok && now.After(exp) {
					delete(expires, order[0])
					order = order[1:]
					continue
				}
				break
			}
			if _, dup := expires[req.id]; dup {
				req.resp <- false
				continue
			}
			if len(order) >= capacity {
				delete(expires, order[0])
				order = order[1:]
			}
			expires[req.id] = now.Add(ttl)
			order = append(order, req.id)
			req.resp <- true
		}
	}
}
