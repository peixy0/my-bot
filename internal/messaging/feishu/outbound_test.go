package feishu

import (
	"testing"
	"time"

	"my-bot/internal/events"
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
