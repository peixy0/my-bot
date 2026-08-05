package tools

import (
	"fmt"
	"sort"

	"my-bot/internal/util"
)

func MarshalResult(v any) string {
	b, err := util.ToJSON(v)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(b)
}

type Registry struct {
	schemas   map[string]ToolSchema
	preparers map[string]ToolPreparer
}

func NewRegistry() *Registry {
	return &Registry{
		schemas:   make(map[string]ToolSchema),
		preparers: make(map[string]ToolPreparer),
	}
}

func (r *Registry) Register(schema ToolSchema, preparer ToolPreparer) {
	r.schemas[schema.Name] = schema
	r.preparers[schema.Name] = preparer
}

type Toolset interface {
	Register(r *Registry)
}

func (r *Registry) RegisterToolset(t Toolset) {
	t.Register(r)
}

func (r *Registry) Get(name string) (ToolPreparer, bool) {
	preparer, ok := r.preparers[name]
	return preparer, ok
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

func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.schemas))
	for k := range r.schemas {
		names = append(names, k)
	}
	return names
}
