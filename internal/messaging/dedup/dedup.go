package dedup

import (
	"context"
	"time"
)

type request struct {
	id   string
	resp chan bool
}

type Dedup struct {
	in chan request
}

func New(ctx context.Context, capacity int, ttl time.Duration) *Dedup {
	d := &Dedup{in: make(chan request)}
	go d.run(ctx, capacity, ttl)
	return d
}

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
