package tools

import (
	"context"
	"fmt"
	"sort"

	"my-bot/internal/util"
)

type ToolResult struct {
	Text   string
	Blocks []map[string]any // non-nil = multimodal content blocks
}

func TextResult(s string) ToolResult            { return ToolResult{Text: s} }
func ImageResult(b []map[string]any) ToolResult { return ToolResult{Blocks: b} }

func MarshalResult(v any) string {
	b, err := util.ToJSON(v)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(b)
}

type ToolHandler func(ctx context.Context, args []byte) (ToolResult, error)

type ToolSchema struct {
	Name          string
	Description   string
	ParameterDesc map[string]any
	Parallel      bool
}

type ToolRegistrar interface {
	RegisterTools(r *Registry)
}

type Registry struct {
	schemas  map[string]ToolSchema
	handlers map[string]ToolHandler
}

func NewRegistry() *Registry {
	return &Registry{
		schemas:  make(map[string]ToolSchema),
		handlers: make(map[string]ToolHandler),
	}
}

func (r *Registry) Register(schema ToolSchema, handler ToolHandler) {
	r.schemas[schema.Name] = schema
	r.handlers[schema.Name] = handler
}

type Toolset interface {
	Register(r *Registry)
}

func (r *Registry) RegisterToolset(t Toolset) {
	t.Register(r)
}

func (r *Registry) Handler(name string) (ToolHandler, bool) {
	h, ok := r.handlers[name]
	return h, ok
}

func (r *Registry) IsParallel(name string) bool {
	s, ok := r.schemas[name]
	return ok && s.Parallel
}

func (r *Registry) Schema(name string) (ToolSchema, bool) {
	s, ok := r.schemas[name]
	return s, ok
}

func (r *Registry) Schemas() []map[string]any {
	names := make([]string, 0, len(r.schemas))
	for name := range r.schemas {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		s := r.schemas[name]
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        s.Name,
				"description": s.Description,
				"parameters":  s.ParameterDesc,
			},
		})
	}
	return out
}

func (r *Registry) Fork() *Registry {
	c := &Registry{
		schemas:  make(map[string]ToolSchema, len(r.schemas)),
		handlers: make(map[string]ToolHandler, len(r.handlers)),
	}
	for k, v := range r.schemas {
		c.schemas[k] = v
	}
	for k, v := range r.handlers {
		c.handlers[k] = v
	}
	return c
}

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.schemas))
	for k := range r.schemas {
		names = append(names, k)
	}
	return names
}
