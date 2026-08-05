package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"my-bot/internal/messaging"
	"my-bot/internal/tools"
)

func (o *Outbound) Register(r *tools.Registry) {
	o.registerAddReaction(r)
	o.registerSendImage(r)
	o.registerSendFile(r)
}

func (o *Outbound) registerAddReaction(r *tools.Registry) {
	r.Register(tools.ToolSchema{
		Name:        "add_reaction",
		Description: "Add an emoji reaction to the triggering message.",
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"emoji": map[string]any{
					"type":        "string",
					"enum":        []string{"OK", "THUMBSUP", "MUSCLE", "LOL", "THINKING", "Shrug", "Fire", "Coffee", "PARTY", "CAKE", "HEART"},
					"description": "The emoji type to react with. OK, THUMBSUP, MUSCLE, LOL, THINKING, Shrug, Fire, Coffee, PARTY, CAKE, HEART",
				},
			},
			"required": []string{"emoji"},
		}),
	}, func(args []byte) (tools.PreparedTool, error) {
		var p struct {
			Emoji string `json:"emoji"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return tools.PreparedTool{}, fmt.Errorf("parse add_reaction args: %w", err)
		}
		if p.Emoji == "" {
			return tools.PreparedTool{}, fmt.Errorf("emoji must not be empty")
		}
		return tools.PreparedTool{
			Description: fmt.Sprintf("Adding %s reaction", p.Emoji),
			Execute: func(ctx context.Context) (tools.ToolResult, error) {
				if err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
					return o.addReaction(ctx, p.Emoji)
				}); err != nil {
					slog.Warn("feishu add reaction failed", "err", err, "chat_id", o.chatID, "message_id", o.messageID)
					return tools.ErrorResult(err), nil
				}
				return tools.TextResult("reaction added"), nil
			},
		}, nil
	})
}

func (o *Outbound) registerSendImage(r *tools.Registry) {
	r.Register(tools.ToolSchema{
		Name:        "send_image",
		Description: "Upload and send an image file to the current chat.",
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
				if len(data) > 10*1024*1024 {
					return tools.ErrorResult(fmt.Errorf("image file too large: %d bytes", len(data))), nil
				}
				if err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
					_, err := o.sendImage(ctx, data)
					return err
				}); err != nil {
					slog.Warn("feishu send image failed", "err", err, "chat_id", o.chatID)
					return tools.ErrorResult(err), nil
				}
				return tools.TextResult(fmt.Sprintf("sent image %s", p.ImagePath)), nil
			},
		}, nil
	})
}

func (o *Outbound) registerSendFile(r *tools.Registry) {
	r.Register(tools.ToolSchema{
		Name:        "send_file",
		Description: "Send a file to the current chat. File size must be under 20 MiB. Send only when explicitly asked.",
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file to send.",
				},
			},
			"required": []string{"file_path"},
		}),
	}, func(args []byte) (tools.PreparedTool, error) {
		var p struct {
			FilePath string `json:"file_path"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return tools.PreparedTool{}, fmt.Errorf("parse send_file args: %w", err)
		}
		if p.FilePath == "" {
			return tools.PreparedTool{}, fmt.Errorf("file_path must not be empty")
		}
		return tools.PreparedTool{
			Description: fmt.Sprintf("Sending file %s", p.FilePath),
			Execute: func(ctx context.Context) (tools.ToolResult, error) {
				data, err := o.rt.ReadRawBytes(ctx, p.FilePath)
				if err != nil {
					return tools.ErrorResult(fmt.Errorf("read file %s for send_file: %w", p.FilePath, err)), nil
				}
				if len(data) > 20*1024*1024 {
					return tools.ErrorResult(fmt.Errorf("file size too large: %d bytes", len(data))), nil
				}
				if err := messaging.CallWithTimeout(ctx, 10*time.Second, func(ctx context.Context) error {
					return o.sendFile(ctx, filepath.Base(p.FilePath), data)
				}); err != nil {
					slog.Warn("feishu send file failed", "err", err, "chat_id", o.chatID)
					return tools.ErrorResult(err), nil
				}
				return tools.TextResult(fmt.Sprintf("sent file %s", p.FilePath)), nil
			},
		}, nil
	})
}
