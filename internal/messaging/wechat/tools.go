package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"my-bot/internal/tools"
)

func (o *Outbound) Register(r *tools.Registry) {
	o.registerSendImage(r)
	o.registerSendFile(r)
}

func (o *Outbound) registerSendImage(r *tools.Registry) {
	r.Register(tools.ToolSchema{
		Name:        "send_image",
		Description: "Upload and send an image file to the WeChat chat.",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"image_path": map[string]any{"type": "string"},
			},
			"required": []string{"image_path"},
		},
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
		if len(data) > 10*1024*1024 {
			return tools.ToolResult{}, fmt.Errorf("image file too large: %d bytes", len(data))
		}
		if err := o.sendImage(ctx, data); err != nil {
			return tools.ToolResult{}, err
		}
		return tools.TextResult(fmt.Sprintf("sent image %s", p.ImagePath)), nil
	})
}

func (o *Outbound) registerSendFile(r *tools.Registry) {
	r.Register(tools.ToolSchema{
		Name:        "send_file",
		Description: "Send a file to the WeChat chat. File size must be under 20 MiB. Send only when explicitly asked.",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"file_path": map[string]any{
					"type":        "string",
					"description": "Absolute path to the file to send.",
				},
			},
			"required": []string{"file_path"},
		},
	}, func(ctx context.Context, args []byte) (tools.ToolResult, error) {
		var p struct {
			FilePath string `json:"file_path"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return tools.ToolResult{}, fmt.Errorf("parse send_file args: %w", err)
		}
		data, err := o.rt.ReadRawBytes(ctx, p.FilePath)
		if err != nil {
			return tools.ToolResult{}, err
		}
		if len(data) > 20*1024*1024 {
			return tools.ToolResult{}, fmt.Errorf("file size too large: %d bytes", len(data))
		}
		if err := o.sendFile(ctx, filepath.Base(p.FilePath), data); err != nil {
			return tools.ToolResult{}, err
		}
		return tools.TextResult(fmt.Sprintf("sent file %s", p.FilePath)), nil
	})
}
