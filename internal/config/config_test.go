package config

import (
	"os"
	"path/filepath"
	"testing"
)

func validConfig() *Config {
	return &Config{
		LLM: LLMConfig{
			APIKey:      "key",
			Temperature: 1,
			TopP:        1,
		},
		Tool: ToolConfig{
			MaxOutputChars: 1000,
		},
		Heartbeat: HeartbeatConfig{
			IntervalSeconds: 60,
		},
		Context: ContextConfig{
			WindowTokens:         128000,
			MaxOutputTokens:      16384,
			CompressionThreshold: 0.7,
		},
		Vision: VisionConfig{
			MaxImageBytes: 1024,
		},
		WebUI: WebUIConfig{
			Enabled: true,
			Host:    "127.0.0.1",
			Port:    8017,
		},
	}
}

func TestConfigValidateAcceptsDefaultsShape(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigValidateRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"llm api key", func(c *Config) { c.LLM.APIKey = "" }},
		{"temperature low", func(c *Config) { c.LLM.Temperature = -0.1 }},
		{"temperature high", func(c *Config) { c.LLM.Temperature = 2.1 }},
		{"top p low", func(c *Config) { c.LLM.TopP = -0.1 }},
		{"top p high", func(c *Config) { c.LLM.TopP = 1.1 }},
		{"top k", func(c *Config) { c.LLM.TopK = -1 }},
		{"container enabled no name", func(c *Config) { c.Container = ContainerConfig{Enabled: true, Runtime: "podman"} }},
		{"container enabled bad runtime", func(c *Config) { c.Container = ContainerConfig{Enabled: true, Name: "x", Runtime: "runc"} }},
		{"tool max output chars", func(c *Config) { c.Tool.MaxOutputChars = 0 }},
		{"heartbeat interval", func(c *Config) { c.Heartbeat.IntervalSeconds = -1 }},
		{"context window tokens", func(c *Config) { c.Context.WindowTokens = 0 }},
		{"max output tokens", func(c *Config) { c.Context.MaxOutputTokens = 0 }},
		{"compression threshold low", func(c *Config) { c.Context.CompressionThreshold = 0 }},
		{"compression threshold high", func(c *Config) { c.Context.CompressionThreshold = 1.1 }},
		{"image size", func(c *Config) { c.Vision.MaxImageBytes = 0 }},
		{"webui host", func(c *Config) { c.WebUI.Host = "" }},
		{"webui port", func(c *Config) { c.WebUI.Port = 70000 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	return path
}

func TestLoadYAMLDefaults(t *testing.T) {
	path := writeTestConfig(t, "llm:\n  api_key: key\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Context.WindowTokens != 128000 {
		t.Fatalf("expected default window_tokens 128000, got %d", cfg.Context.WindowTokens)
	}
	if cfg.Context.MaxOutputTokens != 16384 {
		t.Fatalf("expected default max_output_tokens 16384, got %d", cfg.Context.MaxOutputTokens)
	}
	if cfg.Tool.MaxOutputChars != 100000 {
		t.Fatalf("expected default max_output_chars 100000, got %d", cfg.Tool.MaxOutputChars)
	}
}

func TestLoadYAMLOverrides(t *testing.T) {
	path := writeTestConfig(t, `
llm:
  api_key: key
context:
  window_tokens: 64000
  max_output_tokens: 8192
tool:
  max_output_chars: 50000
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Context.WindowTokens != 64000 {
		t.Fatalf("expected window_tokens 64000, got %d", cfg.Context.WindowTokens)
	}
	if cfg.Context.MaxOutputTokens != 8192 {
		t.Fatalf("expected max_output_tokens 8192, got %d", cfg.Context.MaxOutputTokens)
	}
	if cfg.Tool.MaxOutputChars != 50000 {
		t.Fatalf("expected max_output_chars 50000, got %d", cfg.Tool.MaxOutputChars)
	}
}

func TestForSessionNoOverride(t *testing.T) {
	base := validConfig()
	session := base.ForSession("oc_missing")
	if session.LLM.APIKey != base.LLM.APIKey {
		t.Fatalf("expected same api_key, got %q", session.LLM.APIKey)
	}
	if session == base {
		t.Fatal("expected independent copy, got same pointer")
	}
}

func TestForSessionWithOverride(t *testing.T) {
	base := validConfig()
	model := "gpt-4o-mini"
	temp := 0.3
	base.Sessions = map[string]SessionOverride{
		"oc_test": {
			LLM: &LLMOverride{
				Model:       &model,
				Temperature: &temp,
			},
		},
	}

	session := base.ForSession("oc_test")
	if session.LLM.Model != "gpt-4o-mini" {
		t.Fatalf("expected model gpt-4o-mini, got %s", session.LLM.Model)
	}
	if session.LLM.Temperature != 0.3 {
		t.Fatalf("expected temperature 0.3, got %f", session.LLM.Temperature)
	}
	if session.LLM.APIKey != "key" {
		t.Fatalf("expected inherited api_key 'key', got %q", session.LLM.APIKey)
	}
	if session.Sessions != nil {
		t.Fatal("expected sessions to be nil in merged config")
	}
}

func TestForSessionDoesNotMutateBase(t *testing.T) {
	base := validConfig()
	model := "gpt-4o-mini"
	base.Sessions = map[string]SessionOverride{
		"oc_test": {
			LLM: &LLMOverride{Model: &model},
		},
	}

	_ = base.ForSession("oc_test")
	if base.LLM.Model == "gpt-4o-mini" {
		t.Fatal("override should not mutate base config")
	}
}
