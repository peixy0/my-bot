package messaging

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"my-bot/internal/runtime"
	"my-bot/internal/tools"

	"github.com/gorilla/websocket"
)

type WSOutbound struct {
	conn   *websocket.Conn
	mu     sync.Mutex
	chatID string
	rt     runtime.Runtime
}

func NewWSOutbound(conn *websocket.Conn, chatID string, rt runtime.Runtime) *WSOutbound {
	return &WSOutbound{conn: conn, chatID: chatID, rt: rt}
}

func (o *WSOutbound) Connected(ctx context.Context) {
	if err := o.writeJSON(map[string]any{
		"type":    "connected",
		"chat_id": o.chatID,
	}); err != nil {
		slog.Warn("websocket send connected failed", "chat", o.chatID, "err", err)
	}
}

func (o *WSOutbound) Send(ctx context.Context, text string) {
	if err := o.writeJSON(map[string]any{
		"type":    "message",
		"chat_id": o.chatID,
		"text":    text,
	}); err != nil {
		slog.Warn("websocket send message failed", "chat", o.chatID, "err", err)
	}
}

func (o *WSOutbound) SendDelta(ctx context.Context, text string) {
	if err := o.writeJSON(map[string]any{
		"type":    "message_stream_delta",
		"chat_id": o.chatID,
		"text":    text,
	}); err != nil {
		slog.Warn("websocket send message_stream_delta failed", "chat", o.chatID, "err", err)
	}
}

func (o *WSOutbound) SendFinal(ctx context.Context) {
	if err := o.writeJSON(map[string]any{
		"type":    "message_stream_end",
		"chat_id": o.chatID,
	}); err != nil {
		slog.Warn("websocket send message_stream_end failed", "chat", o.chatID, "err", err)
	}
}

func (o *WSOutbound) StartThinking(ctx context.Context) {
	if err := o.writeJSON(map[string]any{"type": "thinking_start", "chat_id": o.chatID}); err != nil {
		slog.Warn("websocket send thinking_start failed", "chat", o.chatID, "err", err)
	}
}

func (o *WSOutbound) EndThinking(ctx context.Context) {
	if err := o.writeJSON(map[string]any{"type": "thinking_end", "chat_id": o.chatID}); err != nil {
		slog.Warn("websocket send thinking_end failed", "chat", o.chatID, "err", err)
	}
}

func (o *WSOutbound) RegisterTools(r *tools.Registry) {
	r.Register(tools.ToolSchema{
		Name:        "send_image",
		Description: "Send an image file to the WebSocket client.",
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"image_path": map[string]any{"type": "string"},
			},
			"required": []string{"image_path"},
		}),
	}, func(ctx context.Context, args []byte) (tools.ToolResult, error) {
		var p struct {
			ImagePath string `json:"image_path"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return tools.ToolResult{}, fmt.Errorf("parse send_image args: %w", err)
		}
		data, err := o.rt.ReadRawBytes(ctx, p.ImagePath)
		if err != nil {
			return tools.ToolResult{}, err
		}
		mimeType := http.DetectContentType(data)
		b64 := base64.StdEncoding.EncodeToString(data)
		if err := o.writeJSON(map[string]any{
			"type":      "image",
			"chat_id":   o.chatID,
			"data":      b64,
			"mime_type": mimeType,
		}); err != nil {
			return tools.ToolResult{}, err
		}
		return tools.TextResult(fmt.Sprintf("sent %s", p.ImagePath)), nil
	})
}

func (o *WSOutbound) writeJSON(v any) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.conn.WriteJSON(v)
}

func (o *WSOutbound) Ping(deadline time.Duration) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	_ = o.conn.SetWriteDeadline(time.Now().Add(deadline))
	err := o.conn.WriteMessage(websocket.PingMessage, nil)
	_ = o.conn.SetWriteDeadline(time.Time{})
	return err
}
