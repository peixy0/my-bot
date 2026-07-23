package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestBrokerRoutesAuthenticatedRequest(t *testing.T) {
	broker := NewExtensionBroker(Config{BearerToken: "secret", RequestTimeout: time.Second}, nil, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go broker.loop(ctx)

	server := httptest.NewServer(http.HandlerFunc(broker.handleWebSocket))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]any{"type": "authenticate", "version": protocolVersion, "token": "secret"}); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	var authenticated map[string]any
	if err := conn.ReadJSON(&authenticated); err != nil {
		t.Fatalf("read authentication result: %v", err)
	}
	if authenticated["type"] != "authenticated" {
		t.Fatalf("unexpected authentication result: %#v", authenticated)
	}

	result := make(chan struct {
		value json.RawMessage
		err   error
	}, 1)
	go func() {
		value, callErr := broker.Call(context.Background(), "chat:one", "tabs", map[string]any{})
		result <- struct {
			value json.RawMessage
			err   error
		}{value, callErr}
	}()

	var request brokerRequest
	if err := conn.ReadJSON(&request); err != nil {
		t.Fatalf("read browser request: %v", err)
	}
	if request.Type != "request" || request.ScopeID != "chat:one" || request.Action != "tabs" {
		t.Fatalf("unexpected request: %#v", request)
	}
	if err := conn.WriteJSON(brokerResponse{Type: "response", ID: request.ID, Result: json.RawMessage(`{"tabs":[]}`)}); err != nil {
		t.Fatalf("write browser response: %v", err)
	}
	got := <-result
	if got.err != nil {
		t.Fatalf("broker call: %v", got.err)
	}
	if string(got.value) != `{"tabs":[]}` {
		t.Fatalf("unexpected result: %s", got.value)
	}
}

func TestBrokerRejectsInvalidBearerToken(t *testing.T) {
	broker := NewExtensionBroker(Config{BearerToken: "secret"}, nil, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go broker.loop(ctx)

	server := httptest.NewServer(http.HandlerFunc(broker.handleWebSocket))
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial broker: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "authenticate", "version": protocolVersion, "token": "wrong"}); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var frame map[string]any
	if err := conn.ReadJSON(&frame); err == nil {
		t.Fatalf("expected connection close, got %#v", frame)
	}
}
