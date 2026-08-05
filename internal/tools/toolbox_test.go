package tools

import (
	"testing"

	"my-bot/internal/config"
	"my-bot/internal/runtime"
)

func TestDefaultToolPreparersRejectMissingRequiredArguments(t *testing.T) {
	cfg := &config.Config{
		LLM: config.LLMConfig{Vision: true},
		Tool: config.ToolConfig{
			WebSearchAPI:   "https://example.com",
			MaxOutputChars: 4096,
		},
	}
	registry := NewRegistry()
	NewDefaultToolset(runtime.NewHostRuntime(4096), NewSkillLoader(""), cfg).Register(registry)

	for _, name := range []string{
		"web_search",
		"fetch",
		"read_file",
		"write_file",
		"append_file",
		"edit_file",
		"grep",
		"glob",
		"read_image",
		"use_skill",
	} {
		t.Run(name, func(t *testing.T) {
			preparer, ok := registry.Get(name)
			if !ok {
				t.Fatalf("%s was not registered", name)
			}
			if _, err := preparer([]byte(`{}`)); err == nil {
				t.Fatalf("expected %s to reject missing required arguments", name)
			}
		})
	}
}

func TestDefaultToolPreparerRejectsMalformedJSON(t *testing.T) {
	cfg := &config.Config{Tool: config.ToolConfig{MaxOutputChars: 4096}}
	registry := NewRegistry()
	NewDefaultToolset(runtime.NewHostRuntime(4096), NewSkillLoader(""), cfg).Register(registry)
	preparer, ok := registry.Get("read_file")
	if !ok {
		t.Fatal("read_file was not registered")
	}
	if _, err := preparer([]byte(`{`)); err == nil {
		t.Fatal("expected malformed JSON error")
	}
}

func TestEditFilePreparerAllowsEmptyReplacement(t *testing.T) {
	cfg := &config.Config{Tool: config.ToolConfig{MaxOutputChars: 4096}}
	registry := NewRegistry()
	NewDefaultToolset(runtime.NewHostRuntime(4096), NewSkillLoader(""), cfg).Register(registry)
	preparer, ok := registry.Get("edit_file")
	if !ok {
		t.Fatal("edit_file was not registered")
	}
	prepared, err := preparer([]byte(`{"filename":"example.txt","edits":[{"search":"remove me","replace":""}]}`))
	if err != nil {
		t.Fatalf("prepare edit_file deletion: %v", err)
	}
	if prepared.Description == "" || prepared.Execute == nil {
		t.Fatalf("unexpected prepared edit_file: %#v", prepared)
	}
}
