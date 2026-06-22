package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"my-bot/internal/toolkit"
)

func (o *Outbound) RegisterTools(r toolkit.ToolRegistry) {
	o.registerAddReaction(r)
	o.registerSendImage(r)
	o.registerSendFile(r)
}

func (o *Outbound) registerAddReaction(r toolkit.ToolRegistry) {
	r.Register(toolkit.ToolSchema{
		Name:        "add_reaction",
		Description: "Add an emoji reaction to the triggering Feishu message.",
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
	}, func(ctx context.Context, args []byte) (toolkit.ToolResult, error) {
		var p struct {
			Emoji string `json:"emoji"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return toolkit.ToolResult{}, fmt.Errorf("parse add_reaction args: %w", err)
		}
		if err := o.addReaction(ctx, p.Emoji); err != nil {
			return toolkit.ToolResult{}, err
		}
		return toolkit.TextResult("reaction added"), nil
	})
}

func (o *Outbound) registerSendImage(r toolkit.ToolRegistry) {
	r.Register(toolkit.ToolSchema{
		Name:        "send_image",
		Description: "Upload and send an image file to the Feishu chat.",
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"image_path": map[string]any{"type": "string"},
			},
			"required": []string{"image_path"},
		}),
	}, func(ctx context.Context, args []byte) (toolkit.ToolResult, error) {
		var p struct {
			ImagePath string `json:"image_path"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return toolkit.ToolResult{}, fmt.Errorf("parse send_image args: %w", err)
		}
		data, err := o.rt.ReadRawBytes(ctx, p.ImagePath)
		if err != nil {
			return toolkit.ToolResult{}, err
		}
		if len(data) > 10*1024*1024 {
			return toolkit.ToolResult{}, fmt.Errorf("image file too large: %d bytes", len(data))
		}
		if _, err := o.sendImage(ctx, data); err != nil {
			return toolkit.ToolResult{}, err
		}
		return toolkit.TextResult(fmt.Sprintf("sent image %s", p.ImagePath)), nil
	})
}

func (o *Outbound) registerSendFile(r toolkit.ToolRegistry) {
	r.Register(toolkit.ToolSchema{
		Name:        "send_file",
		Description: "Send a file to the Feishu chat. File size must be under 20 MiB. Send only when explicitly asked.",
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
	}, func(ctx context.Context, args []byte) (toolkit.ToolResult, error) {
		var p struct {
			FilePath string `json:"file_path"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return toolkit.ToolResult{}, fmt.Errorf("parse send_file args: %w", err)
		}
		data, err := o.rt.ReadRawBytes(ctx, p.FilePath)
		if err != nil {
			return toolkit.ToolResult{}, err
		}
		if len(data) > 20*1024*1024 {
			return toolkit.ToolResult{}, fmt.Errorf("file size too large: %d bytes", len(data))
		}
		if err := o.sendFile(ctx, filepath.Base(p.FilePath), data); err != nil {
			return toolkit.ToolResult{}, err
		}
		return toolkit.TextResult(fmt.Sprintf("sent file %s", p.FilePath)), nil
	})
}
