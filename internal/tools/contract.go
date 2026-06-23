package tools

import "context"

type ToolResult struct {
	Text   string
	Blocks []map[string]any
}

func TextResult(s string) ToolResult            { return ToolResult{Text: s} }
func ImageResult(b []map[string]any) ToolResult { return ToolResult{Blocks: b} }

type ToolHandler func(ctx context.Context, args []byte) (ToolResult, error)

type ToolSchema struct {
	Name          string
	Description   string
	ParameterDesc map[string]any
	Parallel      bool
}
