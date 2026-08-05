package tools

import (
	"context"
	"testing"
)

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolSchema{Name: "foo", Description: "does foo"}, func([]byte) (PreparedTool, error) {
		return PreparedTool{
			Description: "doing foo...",
			Execute: func(context.Context) (ToolResult, error) {
				return TextResult("foo result"), nil
			},
		}, nil
	})

	preparer, ok := r.Get("foo")
	if !ok {
		t.Fatal("expected preparer to be found")
	}
	prepared, err := preparer([]byte(`{}`))
	if err != nil {
		t.Fatalf("prepare foo: %v", err)
	}
	if prepared.Description != "doing foo..." {
		t.Fatalf("unexpected prepared description: %q", prepared.Description)
	}
	s, ok := r.Schema("foo")
	if !ok || s.Name != "foo" {
		t.Fatal("expected schema to be found")
	}
}

func TestRegistry_PreparerNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Fatal("expected preparer not to be found")
	}
}

func TestRegistry_IsParallel(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolSchema{Name: "parallel_tool", Parallel: true}, nil)
	r.Register(ToolSchema{Name: "sequential_tool", Parallel: false}, nil)

	if !r.IsParallel("parallel_tool") {
		t.Fatal("expected parallel_tool to be parallel")
	}
	if r.IsParallel("sequential_tool") {
		t.Fatal("expected sequential_tool to not be parallel")
	}
	if r.IsParallel("nonexistent") {
		t.Fatal("expected nonexistent tool to not be parallel")
	}
}

func TestRegistry_SchemasSortedByName(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolSchema{Name: "c_tool", Description: "c"}, nil)
	r.Register(ToolSchema{Name: "a_tool", Description: "a"}, nil)
	r.Register(ToolSchema{Name: "b_tool", Description: "b"}, nil)

	schemas := r.Schemas()
	if len(schemas) != 3 {
		t.Fatalf("expected 3 schemas, got %d", len(schemas))
	}
	names := make([]string, len(schemas))
	for i, s := range schemas {
		fn := s["function"].(map[string]any)
		names[i] = fn["name"].(string)
	}
	if names[0] != "a_tool" || names[1] != "b_tool" || names[2] != "c_tool" {
		t.Fatalf("expected sorted names, got %v", names)
	}
}

func TestRegistry_Names(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolSchema{Name: "x"}, nil)
	r.Register(ToolSchema{Name: "y"}, nil)

	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
	// Names is unordered; just check membership
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	if !m["x"] || !m["y"] {
		t.Fatalf("expected x and y, got %v", names)
	}
}

func TestRegistry_SchemaLookup(t *testing.T) {
	r := NewRegistry()
	r.Register(ToolSchema{Name: "grep", Description: "search", ParameterDesc: map[string]any{"type": "object"}}, nil)

	s, ok := r.Schema("grep")
	if !ok {
		t.Fatal("expected schema to be found")
	}
	if s.Name != "grep" || s.Description != "search" {
		t.Fatalf("unexpected schema: %+v", s)
	}
}

func TestRegistry_SchemaNotFound(t *testing.T) {
	r := NewRegistry()
	_, ok := r.Schema("nope")
	if ok {
		t.Fatal("expected schema not to be found")
	}
}

func TestRegistry_RegisterToolset(t *testing.T) {
	r := NewRegistry()
	r.RegisterToolset(&testToolset{})

	_, ok := r.Get("from_toolset")
	if !ok {
		t.Fatal("expected toolset preparer to be registered")
	}
	s, ok := r.Schema("from_toolset")
	if !ok || s.Description != "toolset tool" {
		t.Fatalf("expected toolset schema, got %+v", s)
	}
}

type testToolset struct{}

func (t *testToolset) Register(r *Registry) {
	r.Register(ToolSchema{Name: "from_toolset", Description: "toolset tool"}, func([]byte) (PreparedTool, error) {
		return PreparedTool{Description: "using toolset tool...", Execute: func(context.Context) (ToolResult, error) {
			return TextResult("done"), nil
		}}, nil
	})
}
