package actor

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
)

// Hub serializes writes for a single room. Callbacks must not call Hub.Call
// for the same room, or the mailbox deadlocks.
type Hub struct {
	mu    sync.Mutex
	boxes map[string]*mailbox
}

type mailbox struct {
	ch chan job
}

type job struct {
	ctx  context.Context
	fn   func(context.Context) (any, error)
	resp chan result
}

type result struct {
	val any
	err error
}

func New() *Hub {
	return &Hub{boxes: map[string]*mailbox{}}
}

func (h *Hub) Call(ctx context.Context, roomID string, fn func(context.Context) (any, error)) (any, error) {
	if roomID == "" {
		return nil, fmt.Errorf("room id is empty")
	}
	mb := h.get(roomID)
	resp := make(chan result, 1)
	select {
	case mb.ch <- job{ctx: ctx, fn: fn, resp: resp}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case r := <-resp:
		return r.val, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (h *Hub) get(roomID string) *mailbox {
	h.mu.Lock()
	defer h.mu.Unlock()
	mb := h.boxes[roomID]
	if mb == nil {
		mb = &mailbox{ch: make(chan job, 8)}
		h.boxes[roomID] = mb
		go mb.loop()
	}
	return mb
}

func (m *mailbox) loop() {
	for job := range m.ch {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					job.resp <- result{err: fmt.Errorf("room actor panic: %v\n%s", rec, debug.Stack())}
				}
			}()
			val, err := job.fn(job.ctx)
			job.resp <- result{val: val, err: err}
		}()
	}
}
