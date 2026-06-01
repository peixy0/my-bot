package feishu

import (
	"context"
	"time"
)

type Config struct {
	AppID             string
	AppSecret         string
	EncryptKey        string
	VerificationToken string
}

const (
	dedupCapacity = 1024
	dedupTTL      = 5 * time.Minute
)

type dedup struct {
	in chan dedupReq
}

type dedupReq struct {
	id   string
	resp chan bool
}

func newDedup(ctx context.Context, capacity int, ttl time.Duration) *dedup {
	d := &dedup{in: make(chan dedupReq)}
	go d.run(ctx, capacity, ttl)
	return d
}

func (d *dedup) check(id string) bool {
	resp := make(chan bool, 1)
	select {
	case d.in <- dedupReq{id: id, resp: resp}:
		return <-resp
	case <-time.After(time.Second):
		return true
	}
}

func (d *dedup) run(ctx context.Context, capacity int, ttl time.Duration) {
	expires := make(map[string]time.Time, capacity)
	order := make([]string, 0, capacity)
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-d.in:
			now := time.Now()
			for len(order) > 0 {
				exp, ok := expires[order[0]]
				if ok && now.After(exp) {
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
