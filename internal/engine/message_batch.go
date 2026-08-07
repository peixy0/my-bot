package engine

import (
	"encoding/base64"
	"fmt"
	"strings"

	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/llm"
)

func drainMessageInbox(box inbox.Inbox[events.MessageEvent], initial ...events.MessageEvent) []events.MessageEvent {
	items := append([]events.MessageEvent(nil), initial...)
	if box == nil {
		return items
	}
	for {
		msg, ok := box.TryReceive()
		if !ok {
			return items
		}
		items = append(items, msg)
	}
}

func buildMessageBatch(items []events.MessageEvent, visionSupport bool, preface string) *llm.ChatMessage {
	var parts []llm.ContentPart
	previousTextPart := ""
	appendText := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if previousTextPart != "" {
			previousTextPart += "\n\n" + text
			return
		}
		previousTextPart = text
	}
	flushText := func() {
		if previousTextPart == "" {
			return
		}
		parts = append(parts, llm.ContentPart{"type": "text", "text": previousTextPart})
		previousTextPart = ""
	}

	appendText(preface)
	hasImage := false
	hasMessage := false
	for _, item := range items {
		switch ev := item.(type) {
		case events.TextInputEvent:
			appendText(formatMessageInput(ev.MessageID, ev.Message))
			hasMessage = true
		case events.ImageInputEvent:
			if !visionSupport {
				continue
			}
			appendText(formatMessageInput(ev.MessageID, ev.Message))
			for _, image := range ev.ImageData {
				flushText()
				parts = appendVisionImagePart(parts, image)
				hasImage = true
			}
			hasMessage = true
		}
	}
	if !hasMessage {
		return nil
	}
	if !hasImage {
		msg := llm.UserMessage(previousTextPart)
		return &msg
	}
	flushText()
	return &llm.ChatMessage{Role: "user", Content: parts}
}

func formatMessageInput(messageID, message string) string {
	if messageID == "" {
		return message
	}
	if message == "" {
		return fmt.Sprintf("MESSAGE ID: %s", messageID)
	}
	return fmt.Sprintf("MESSAGE ID: %s\n\n%s", messageID, message)
}

func messageEventSender(event events.MessageEvent) events.Outbound {
	switch ev := event.(type) {
	case events.TextInputEvent:
		return ev.Sender
	case events.ImageInputEvent:
		return ev.Sender
	default:
		return nil
	}
}

func appendVisionImagePart(parts []llm.ContentPart, image events.ImageData) []llm.ContentPart {
	return append(parts, llm.ContentPart{
		"type": "image_url",
		"image_url": map[string]any{
			"url":    fmt.Sprintf("data:%s;base64,%s", image.MIMEType, base64.StdEncoding.EncodeToString(image.Data)),
			"detail": "auto",
		},
	})
}
