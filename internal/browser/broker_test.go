package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestBrokerRoutesSingleFrameResponse(t *testing.T) {
	broker := NewExtensionBroker(Config{}, nil, 0)
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

	if err := conn.WriteJSON(map[string]any{"type": "authenticate", "version": protocolVersion}); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	var authenticated map[string]any
	if err := conn.ReadJSON(&authenticated); err != nil {
		t.Fatalf("read authentication result: %v", err)
	}
	if authenticated["type"] != "authenticated" {
		t.Fatalf("unexpected authentication result: %#v", authenticated)
	}

	type callResult struct {
		data string
		err  error
	}
	result := make(chan callResult, 1)
	go func() {
		frames, callErr := broker.Call(context.Background(), "chat:one", "tabs", map[string]any{})
		if callErr != nil {
			result <- callResult{err: callErr}
			return
		}
		var sb strings.Builder
		for frame := range frames {
			if frame.err != nil {
				result <- callResult{err: frame.err}
				return
			}
			sb.WriteString(frame.data)
		}
		result <- callResult{data: sb.String()}
	}()

	var request brokerRequest
	if err := conn.ReadJSON(&request); err != nil {
		t.Fatalf("read browser request: %v", err)
	}
	if request.Type != "request" || request.ScopeID != "chat:one" || request.Action != "tabs" {
		t.Fatalf("unexpected request: %#v", request)
	}
	if err := conn.WriteJSON(brokerResponse{ID: request.ID, Data: `{"tabs":[]}`}); err != nil {
		t.Fatalf("write browser response: %v", err)
	}
	got := <-result
	if got.err != nil {
		t.Fatalf("broker call: %v", got.err)
	}
	if got.data != `{"tabs":[]}` {
		t.Fatalf("unexpected result: %s", got.data)
	}
}

func TestBrokerStreamsChunkedResponse(t *testing.T) {
	broker := NewExtensionBroker(Config{}, nil, 0)
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

	if err := conn.WriteJSON(map[string]any{"type": "authenticate", "version": protocolVersion}); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	var authenticated map[string]any
	if err := conn.ReadJSON(&authenticated); err != nil {
		t.Fatalf("read authentication result: %v", err)
	}

	type callResult struct {
		data string
		err  error
	}
	result := make(chan callResult, 1)
	go func() {
		frames, callErr := broker.Call(context.Background(), "chat:one", "screenshot", map[string]any{})
		if callErr != nil {
			result <- callResult{err: callErr}
			return
		}
		var sb strings.Builder
		for frame := range frames {
			if frame.err != nil {
				result <- callResult{err: frame.err}
				return
			}
			sb.WriteString(frame.data)
		}
		result <- callResult{data: sb.String()}
	}()

	var request brokerRequest
	if err := conn.ReadJSON(&request); err != nil {
		t.Fatalf("read browser request: %v", err)
	}
	if err := conn.WriteJSON(brokerResponse{ID: request.ID, Data: "part1-", HasMore: true}); err != nil {
		t.Fatalf("write chunk 1: %v", err)
	}
	if err := conn.WriteJSON(brokerResponse{ID: request.ID, Data: "part2-", HasMore: true}); err != nil {
		t.Fatalf("write chunk 2: %v", err)
	}
	if err := conn.WriteJSON(brokerResponse{ID: request.ID, Data: "part3"}); err != nil {
		t.Fatalf("write final chunk: %v", err)
	}
	got := <-result
	if got.err != nil {
		t.Fatalf("broker call: %v", got.err)
	}
	expected := "part1-part2-part3"
	if got.data != expected {
		t.Fatalf("unexpected result: %s, want %s", got.data, expected)
	}
}
