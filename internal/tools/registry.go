package tools

import (
	"fmt"
	"sort"

	"my-bot/internal/toolkit"
	"my-bot/internal/util"
)

type ToolResult = toolkit.ToolResult
type ToolSchema = toolkit.ToolSchema
type ToolHandler = toolkit.ToolHandler

var TextResult = toolkit.TextResult
var ImageResult = toolkit.ImageResult

func MarshalResult(v any) string {
	b, err := util.ToJSON(v)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(b)
}

type Registry struct {
	schemas  map[string]toolkit.ToolSchema
	handlers map[string]toolkit.ToolHandler
}

func NewRegistry() *Registry {
	return &Registry{
		schemas:  make(map[string]toolkit.ToolSchema),
		handlers: make(map[string]toolkit.ToolHandler),
	}
}

func (r *Registry) Register(schema toolkit.ToolSchema, handler toolkit.ToolHandler) {
	r.schemas[schema.Name] = schema
	r.handlers[schema.Name] = handler
}

type Toolset interface {
	Register(r *Registry)
}

func (r *Registry) RegisterToolset(t Toolset) {
	t.Register(r)
}

func (r *Registry) Handler(name string) (toolkit.ToolHandler, bool) {
	h, ok := r.handlers[name]
	return h, ok
}

func (r *Registry) IsParallel(name string) bool {
	s, ok := r.schemas[name]
	return ok && s.Parallel
}

func (r *Registry) Schema(name string) (toolkit.ToolSchema, bool) {
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

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.schemas))
	for k := range r.schemas {
		names = append(names, k)
	}
	return names
}
