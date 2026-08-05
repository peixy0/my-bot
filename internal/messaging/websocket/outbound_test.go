package websocket

import (
	"testing"
	"time"

	"my-bot/internal/events"
)

func TestStreamEndMessageIncludesMetadata(t *testing.T) {
	message := streamEndMessage("chat-1", &events.ResponseMetadata{
		Model:            "test-model",
		PromptTokens:     900,
		CompletionTokens: 100,
		TotalTokens:      1000,
		ContextWindow:    128000,
		GenerationTime:   2 * time.Second,
	})

	if message["type"] != "message_stream_end" || message["chat_id"] != "chat-1" {
		t.Fatalf("unexpected stream end message: %#v", message)
	}
	metadata, ok := message["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map, got %#v", message["metadata"])
	}
	if metadata["model"] != "test-model" ||
		metadata["prompt_tokens"] != int64(900) ||
		metadata["completion_tokens"] != int64(100) ||
		metadata["total_tokens"] != int64(1000) ||
		metadata["context_window"] != int64(128000) ||
		metadata["generation_seconds"] != 2.0 {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
}

func TestStreamEndMessageOmitsMetadata(t *testing.T) {
	message := streamEndMessage("chat-1", nil)

	if _, exists := message["metadata"]; exists {
		t.Fatalf("expected metadata to be omitted, got %#v", message)
	}
}
