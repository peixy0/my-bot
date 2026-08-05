package tools

import "context"

type ToolResult struct {
	Text   string
	Blocks []map[string]any
}

func ErrorResult(err error) ToolResult {
	return ToolResult{Text: "error: " + err.Error()}
}

func TextResult(s string) ToolResult            { return ToolResult{Text: s} }
func ImageResult(b []map[string]any) ToolResult { return ToolResult{Blocks: b} }

type PreparedTool struct {
	Description string
	Execute     func(ctx context.Context) (ToolResult, error)
}

type ToolPreparer func(args []byte) (PreparedTool, error)

type ToolSchema struct {
	Name          string
	Description   string
	ParameterDesc map[string]any
	Parallel      bool
}
