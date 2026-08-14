package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestServerServesFrontendAssetsAndFallback(t *testing.T) {
	assets := t.TempDir()
	if err := os.Mkdir(filepath.Join(assets, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "index.html"), []byte("<main>webui</main>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "assets", "app-123.js"), []byte("app()"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServer(inbox.NewMemory[events.AgentEvent](1), nil, ServerOptions{
		Token:      "secret",
		AssetsPath: assets,
	})

	tests := []struct {
		name       string
		path       string
		statusCode int
		body       string
	}{
		{name: "public index", path: "/", statusCode: http.StatusOK, body: "<main>webui</main>"},
		{name: "hashed asset", path: "/assets/app-123.js", statusCode: http.StatusOK, body: "app()"},
		{name: "spa fallback", path: "/sessions/current", statusCode: http.StatusOK, body: "<main>webui</main>"},
		{name: "missing asset", path: "/assets/missing.js", statusCode: http.StatusNotFound},
		{name: "unknown api", path: "/api/missing", statusCode: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, req)
			if rec.Code != tt.statusCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.statusCode)
			}
			if tt.body != "" && rec.Body.String() != tt.body {
				t.Fatalf("body = %q, want %q", rec.Body.String(), tt.body)
			}
		})
	}
}

func TestServerProtectsOnlyWebSocket(t *testing.T) {
	assets := t.TempDir()
	if err := os.WriteFile(filepath.Join(assets, "index.html"), []byte("webui"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewServer(inbox.NewMemory[events.AgentEvent](1), nil, ServerOptions{
		Token:      "secret",
		AssetsPath: assets,
	})

	root := httptest.NewRecorder()
	s.mux.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/", nil))
	if root.Code != http.StatusOK {
		t.Fatalf("root status = %d, want %d", root.Code, http.StatusOK)
	}

	ws := httptest.NewRecorder()
	s.mux.ServeHTTP(ws, httptest.NewRequest(http.MethodGet, "/api/bot", nil))
	if ws.Code != http.StatusUnauthorized {
		t.Fatalf("websocket status = %d, want %d", ws.Code, http.StatusUnauthorized)
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
