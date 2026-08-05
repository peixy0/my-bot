package websocket

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"my-bot/internal/events"
	"my-bot/internal/runtime"
	"my-bot/internal/tools"

	"github.com/gorilla/websocket"
)

type Outbound struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	chatID string
	rt     runtime.Runtime
}

func NewOutbound(conn *websocket.Conn, chatID string, rt runtime.Runtime) *Outbound {
	return &Outbound{conn: conn, chatID: chatID, rt: rt}
}

func (o *Outbound) Connected(ctx context.Context) {
	if err := o.writeJSON(map[string]any{
		"type":    "connected",
		"chat_id": o.chatID,
	}); err != nil {
		slog.Warn("websocket send connected failed", "chat_id", o.chatID, "err", err)
	}
}

func (o *Outbound) Send(ctx context.Context, text string) {
	if err := o.writeJSON(map[string]any{
		"type":    "message",
		"chat_id": o.chatID,
		"text":    text,
	}); err != nil {
		slog.Warn("websocket send message failed", "chat_id", o.chatID, "err", err)
	}
}

func (o *Outbound) SendBegin(ctx context.Context) {
	if err := o.writeJSON(map[string]any{
		"type":    "message_stream_begin",
		"chat_id": o.chatID,
	}); err != nil {
		slog.Warn("websocket send message_stream_begin failed", "chat_id", o.chatID, "err", err)
	}
}

func (o *Outbound) SendDelta(ctx context.Context, text string) {
	if err := o.writeJSON(map[string]any{
		"type":    "message_stream_delta",
		"chat_id": o.chatID,
		"text":    text,
	}); err != nil {
		slog.Warn("websocket send message_stream_delta failed", "chat_id", o.chatID, "err", err)
	}
}

func (o *Outbound) SendFinal(ctx context.Context, metadata *events.ResponseMetadata) {
	if err := o.writeJSON(streamEndMessage(o.chatID, metadata)); err != nil {
		slog.Warn("websocket send message_stream_end failed", "chat_id", o.chatID, "err", err)
	}
}

func streamEndMessage(chatID string, metadata *events.ResponseMetadata) map[string]any {
	message := map[string]any{
		"type":    "message_stream_end",
		"chat_id": chatID,
	}
	if metadata != nil {
		message["metadata"] = map[string]any{
			"model":              metadata.Model,
			"prompt_tokens":      metadata.PromptTokens,
			"completion_tokens":  metadata.CompletionTokens,
			"total_tokens":       metadata.TotalTokens,
			"context_window":     metadata.ContextWindow,
			"generation_seconds": metadata.GenerationTime.Seconds(),
		}
	}
	return message
}

func (o *Outbound) StartThinking(ctx context.Context) {
	if err := o.writeJSON(map[string]any{"type": "thinking_start", "chat_id": o.chatID}); err != nil {
		slog.Warn("websocket send thinking_start failed", "chat_id", o.chatID, "err", err)
	}
}

func (o *Outbound) EndThinking(ctx context.Context) {
	if err := o.writeJSON(map[string]any{"type": "thinking_end", "chat_id": o.chatID}); err != nil {
		slog.Warn("websocket send thinking_end failed", "chat_id", o.chatID, "err", err)
	}
}

func (o *Outbound) Register(r *tools.Registry) {
	r.Register(tools.ToolSchema{
		Name:        "send_image",
		Description: "Send an image file to the current chat.",
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"image_path": map[string]any{"type": "string"},
			},
			"required": []string{"image_path"},
		}),
	}, func(args []byte) (tools.PreparedTool, error) {
		var p struct {
			ImagePath string `json:"image_path"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return tools.PreparedTool{}, fmt.Errorf("parse send_image args: %w", err)
		}
		if p.ImagePath == "" {
			return tools.PreparedTool{}, fmt.Errorf("image_path must not be empty")
		}
		return tools.PreparedTool{
			Description: fmt.Sprintf("Sending image %s", p.ImagePath),
			Execute: func(ctx context.Context) (tools.ToolResult, error) {
				data, err := o.rt.ReadRawBytes(ctx, p.ImagePath)
				if err != nil {
					return tools.ErrorResult(fmt.Errorf("read image %s for send_image: %w", p.ImagePath, err)), nil
				}
				mimeType := http.DetectContentType(data)
				b64 := base64.StdEncoding.EncodeToString(data)
				if err := o.writeJSON(map[string]any{
					"type":      "image",
					"chat_id":   o.chatID,
					"data":      b64,
					"mime_type": mimeType,
				}); err != nil {
					return tools.ErrorResult(fmt.Errorf("send image %s to current chat: %w", p.ImagePath, err)), nil
				}
				return tools.TextResult(fmt.Sprintf("sent %s", p.ImagePath)), nil
			},
		}, nil
	})
}

func (o *Outbound) writeJSON(v any) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.conn.WriteJSON(v)
}

func (o *Outbound) Ping(deadline time.Duration) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	_ = o.conn.SetWriteDeadline(time.Now().Add(deadline))
	err := o.conn.WriteMessage(websocket.PingMessage, nil)
	_ = o.conn.SetWriteDeadline(time.Time{})
	return err
}
