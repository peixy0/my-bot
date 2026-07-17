package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"my-bot/internal/events"
	"my-bot/internal/inbox"
)

func TestServerAuthorized(t *testing.T) {
	s := NewServer(inbox.NewMemory[events.AgentEvent](1), nil, ServerOptions{Token: "secret"})

	req := httptest.NewRequest(http.MethodGet, "/api/bot?token=secret", nil)
	if !s.authorized(req) {
		t.Fatal("expected query token to authorize")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/bot", nil)
	req.Header.Set("Authorization", "Bearer secret")
	if !s.authorized(req) {
		t.Fatal("expected bearer token to authorize")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/bot?token=wrong", nil)
	if s.authorized(req) {
		t.Fatal("expected wrong token to be rejected")
	}
}

func TestServerEnqueueCanceled(t *testing.T) {
	agentInbox := inbox.NewMemory[events.AgentEvent](0)
	s := NewServer(agentInbox, nil, ServerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if s.enqueue(ctx, events.TextInputEvent{ChatID: "c"}) {
		t.Fatal("expected enqueue to fail on canceled context")
	}
}

func TestServerEnqueueSuccess(t *testing.T) {
	agentInbox := inbox.NewMemory[events.AgentEvent](1)
	s := NewServer(agentInbox, nil, ServerOptions{})

	if !s.enqueue(context.Background(), events.TextInputEvent{ChatID: "c"}) {
		t.Fatal("expected enqueue to succeed")
	}
}
