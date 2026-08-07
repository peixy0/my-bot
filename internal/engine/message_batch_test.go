package engine

import (
	"strings"
	"testing"

	"my-bot/internal/events"
	"my-bot/internal/inbox"
)

func TestDrainMessageInboxIncludesInitialAndBufferedMessages(t *testing.T) {
	box := inbox.NewMemory[events.MessageEvent](4)
	box.TryPublish(events.TextInputEvent{Message: "second"})
	box.TryPublish(events.TextInputEvent{Message: "third"})

	items := drainMessageInbox(box, events.TextInputEvent{Message: "first"})

	if len(items) != 3 {
		t.Fatalf("expected three messages, got %d", len(items))
	}
	if box.Len() != 0 {
		t.Fatalf("expected inbox to be empty, got %d", box.Len())
	}
}

func TestBuildMessageBatchTextIncludesMessageIDs(t *testing.T) {
	msg := buildMessageBatch([]events.MessageEvent{
		events.TextInputEvent{MessageID: "m1", Message: "first"},
		events.TextInputEvent{MessageID: "m2", Message: "second"},
	}, false, "MESSAGE TIME: now")

	if msg == nil {
		t.Fatal("expected message")
	}
	text, ok := msg.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", msg.Content)
	}
	for _, want := range []string{"MESSAGE TIME: now", "MESSAGE ID: m1", "first", "MESSAGE ID: m2", "second"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in %q", want, text)
		}
	}
}

func TestBuildMessageBatchPreservesMixedOrder(t *testing.T) {
	msg := buildMessageBatch([]events.MessageEvent{
		events.TextInputEvent{MessageID: "m1", Message: "before"},
		events.ImageInputEvent{
			MessageID: "m2",
			Message:   "image",
			ImageData: []events.ImageData{{Data: []byte{1, 2, 3}, MIMEType: "image/png"}},
		},
		events.TextInputEvent{MessageID: "m3", Message: "after"},
	}, true, "")

	parts, ok := msg.Content.([]map[string]any)
	if !ok {
		t.Fatalf("expected multipart content, got %T", msg.Content)
	}
	if len(parts) != 3 {
		t.Fatalf("expected text, image, text parts, got %d", len(parts))
	}
	first := parts[0]["text"].(string)
	last := parts[2]["text"].(string)
	if !strings.Contains(first, "m1") || !strings.Contains(first, "m2") {
		t.Fatalf("expected first two events before image, got %q", first)
	}
	if parts[1]["type"] != "image_url" {
		t.Fatalf("expected image in the middle, got %+v", parts[1])
	}
	if !strings.Contains(last, "m3") || !strings.Contains(last, "after") {
		t.Fatalf("expected final text after image, got %q", last)
	}
}

func TestBuildMessageBatchVisionDisabledKeepsText(t *testing.T) {
	msg := buildMessageBatch([]events.MessageEvent{
		events.ImageInputEvent{MessageID: "image", ImageData: []events.ImageData{{Data: []byte{1}, MIMEType: "image/png"}}},
		events.TextInputEvent{MessageID: "text", Message: "keep me"},
	}, false, "")

	if msg == nil {
		t.Fatal("expected text message")
	}
	text := msg.Content.(string)
	if strings.Contains(text, "image") || !strings.Contains(text, "keep me") {
		t.Fatalf("unexpected text content %q", text)
	}
}

func TestBuildMessageBatchVisionDisabledRejectsImagesOnly(t *testing.T) {
	msg := buildMessageBatch([]events.MessageEvent{
		events.ImageInputEvent{MessageID: "image", ImageData: []events.ImageData{{Data: []byte{1}, MIMEType: "image/png"}}},
	}, false, "MESSAGE TIME: now")

	if msg != nil {
		t.Fatalf("expected no message, got %#v", msg)
	}
}
