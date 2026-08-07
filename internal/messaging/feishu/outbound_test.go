package feishu

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"my-bot/internal/events"
	"my-bot/internal/tools"

	lark "github.com/larksuite/oapi-sdk-go/v3"
)

func TestStreamingCardPayload(t *testing.T) {
	payload := cardPayload("hello", "", true)

	if payload["schema"] != "2.0" {
		t.Fatalf("expected schema 2.0, got %#v", payload["schema"])
	}
	config, ok := payload["config"].(map[string]any)
	if !ok {
		t.Fatalf("expected config map, got %#v", payload["config"])
	}
	if config["streaming_mode"] != true {
		t.Fatalf("expected streaming mode enabled, got %#v", config["streaming_mode"])
	}
	if config["update_multi"] != true {
		t.Fatalf("expected update_multi enabled, got %#v", config["update_multi"])
	}
	body, ok := payload["body"].(map[string]any)
	if !ok {
		t.Fatalf("expected body map, got %#v", payload["body"])
	}
	elements, ok := body["elements"].([]map[string]any)
	if !ok || len(elements) != 1 {
		t.Fatalf("expected only markdown element (no footer in streaming mode), got %#v", body["elements"])
	}
	element := elements[0]
	if element["tag"] != "markdown" || element["content"] != "hello" || element["element_id"] != streamingElementID {
		t.Fatalf("unexpected streaming element: %#v", element)
	}
}

func TestCardPayloadStaticFooter(t *testing.T) {
	payload := cardPayload("hello", "stats", false)
	body := payload["body"].(map[string]any)
	elements := body["elements"].([]map[string]any)

	if len(elements) != 2 {
		t.Fatalf("expected static footer, got %#v", elements)
	}
	text := elements[1]["text"].(map[string]any)
	if text["content"] != "stats" {
		t.Fatalf("expected footer content, got %#v", text)
	}
}

func TestCardPayloadOmitsEmptyStaticFooter(t *testing.T) {
	payload := cardPayload("hello", "", false)
	body := payload["body"].(map[string]any)
	elements := body["elements"].([]map[string]any)

	if len(elements) != 1 {
		t.Fatalf("expected no static footer, got %#v", elements)
	}
}

func TestFormatResponseMetadata(t *testing.T) {
	got := formatResponseMetadata(&events.ResponseMetadata{
		Model:            "test-model",
		CompletionTokens: 84,
		TotalTokens:      1234,
		ContextWindow:    128000,
		GenerationTime:   2 * time.Second,
	})
	want := "test-model · 42.0 tokens/s · 1,234 / 128,000 tokens (1.0%)"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestFormatResponseMetadataWithoutContextWindow(t *testing.T) {
	got := formatResponseMetadata(&events.ResponseMetadata{TotalTokens: 1234})
	if got != "1,234 tokens" {
		t.Fatalf("unexpected metadata: %q", got)
	}
}

func TestFormatResponseMetadataOmitsUnavailableValues(t *testing.T) {
	if got := formatResponseMetadata(nil); got != "" {
		t.Fatalf("expected nil metadata to be empty, got %q", got)
	}
	if got := formatResponseMetadata(&events.ResponseMetadata{}); got != "" {
		t.Fatalf("expected zero metadata to be empty, got %q", got)
	}
}

func TestAddReactionRequiresExplicitMessageID(t *testing.T) {
	reg := tools.NewRegistry()
	NewOutbound(nil, nil, "chat-1").Register(reg)
	preparer, ok := reg.Get("add_reaction")
	if !ok {
		t.Fatal("expected add_reaction tool")
	}
	if _, err := preparer([]byte(`{"emoji":"OK"}`)); err == nil {
		t.Fatal("expected missing message_id to fail")
	}
	if _, err := preparer([]byte(`{"message_id":"message-2","emoji":"OK"}`)); err != nil {
		t.Fatalf("prepare add_reaction: %v", err)
	}
}

func TestAddReactionUsesProvidedMessageID(t *testing.T) {
	var reactionPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "auth") {
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"token","expire":7200}`))
			return
		}
		reactionPath = r.URL.Path
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer server.Close()

	client := lark.NewClient("app", "secret", lark.WithOpenBaseUrl(server.URL))
	outbound := NewOutbound(client, nil, "chat-1")
	if err := outbound.addReaction(context.Background(), "message-2", "OK"); err != nil {
		t.Fatalf("add reaction: %v", err)
	}
	if !strings.Contains(reactionPath, "/messages/message-2/reactions") {
		t.Fatalf("expected target message ID in path, got %q", reactionPath)
	}
}
