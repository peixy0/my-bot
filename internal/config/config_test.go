package config

import (
	"os"
	"path/filepath"
	"testing"
)

func validConfig() *Config {
	return &Config{
		LLM: LLMConfig{
			APIKey:        "key",
			Temperature:   1,
			TopP:          1,
			ContextWindow: 128000,
		},
		Tool: ToolConfig{
			MaxOutputChars: 1000,
		},
		Heartbeat: HeartbeatConfig{
			IntervalSeconds: 60,
		},
		Context: ContextConfig{
			MaxImageBytes:        1024,
			MaxOutputTokens:      16384,
			CompressionThreshold: 0.7,
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
		{"container enabled no name", func(c *Config) { c.Container = &ContainerConfig{Runtime: "podman"} }},
		{"container enabled bad runtime", func(c *Config) { c.Container = &ContainerConfig{Name: "x", Runtime: "runc"} }},
		{"tool max output chars", func(c *Config) { c.Tool.MaxOutputChars = 0 }},
		{"heartbeat interval", func(c *Config) { c.Heartbeat.IntervalSeconds = -1 }},
		{"llm context window", func(c *Config) { c.LLM.ContextWindow = 0 }},
		{"max output tokens", func(c *Config) { c.Context.MaxOutputTokens = 0 }},
		{"compression threshold low", func(c *Config) { c.Context.CompressionThreshold = 0 }},
		{"compression threshold high", func(c *Config) { c.Context.CompressionThreshold = 1.1 }},
		{"image size", func(c *Config) { c.Context.MaxImageBytes = 0 }},
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
	if cfg.LLM.ContextWindow != 128000 {
		t.Fatalf("expected default llm.context_window 128000, got %d", cfg.LLM.ContextWindow)
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
  context_window: 64000
context:
  max_output_tokens: 8192
tool:
  max_output_chars: 50000
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.LLM.ContextWindow != 64000 {
		t.Fatalf("expected context_window 64000, got %d", cfg.LLM.ContextWindow)
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

func TestLoadYAMLExtraBody(t *testing.T) {
	path := writeTestConfig(t, `
llm:
  api_key: key
  extra_body:
    chat_template_kwargs:
      enable_thinking: true
    presence_penalty: 0.5
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.LLM.ExtraBody == nil {
		t.Fatal("expected extra_body to be parsed")
	}
	ctk, ok := cfg.LLM.ExtraBody["chat_template_kwargs"].(map[string]any)
	if !ok || ctk["enable_thinking"] != true {
		t.Fatalf("expected chat_template_kwargs.enable_thinking=true, got %v", cfg.LLM.ExtraBody["chat_template_kwargs"])
	}
	if cfg.LLM.ExtraBody["presence_penalty"] != 0.5 {
		t.Fatalf("expected presence_penalty=0.5, got %v", cfg.LLM.ExtraBody["presence_penalty"])
	}
}

func TestModelConfigApplyTo(t *testing.T) {
	target := LLMConfig{
		Temperature: 1.0,
		TopP:        0.95,
		TopK:        0,
		ExtraBody:   map[string]any{"old": true},
	}

	t.Run("all fields set", func(t *testing.T) {
		tgt := target
		temp := 0.6
		topP := 0.8
		topK := 50
		preset := ModelConfig{
			Temperature: &temp,
			TopP:        &topP,
			TopK:        &topK,
			ExtraBody:   map[string]any{"new": true},
		}
		preset.ApplyTo(&tgt)
		if tgt.Temperature != 0.6 {
			t.Fatalf("expected temperature 0.6, got %f", tgt.Temperature)
		}
		if tgt.TopP != 0.8 {
			t.Fatalf("expected top_p 0.8, got %f", tgt.TopP)
		}
		if tgt.TopK != 50 {
			t.Fatalf("expected top_k 50, got %d", tgt.TopK)
		}
		if tgt.ExtraBody["new"] == nil {
			t.Fatal("expected extra_body to be replaced")
		}
		if _, ok := tgt.ExtraBody["old"]; ok {
			t.Fatal("expected extra_body to be fully replaced, not merged")
		}
	})

	t.Run("nil fields do not override", func(t *testing.T) {
		tgt := target
		preset := ModelConfig{}
		preset.ApplyTo(&tgt)
		if tgt.Temperature != 1.0 {
			t.Fatalf("expected temperature unchanged 1.0, got %f", tgt.Temperature)
		}
		if tgt.TopP != 0.95 {
			t.Fatalf("expected top_p unchanged 0.95, got %f", tgt.TopP)
		}
		if tgt.TopK != 0 {
			t.Fatalf("expected top_k unchanged 0, got %d", tgt.TopK)
		}
		if tgt.ExtraBody["old"] == nil {
			t.Fatal("expected extra_body unchanged")
		}
	})
}

func TestForSessionAppliesModelPreset(t *testing.T) {
	base := validConfig()
	base.LLM.Model = "Qwen3-32B"
	temp := 0.6
	base.Models = map[string]ModelConfig{
		"Qwen3-32B": {
			Temperature: &temp,
		},
	}

	session := base.ForSession("oc_no_override")
	if session.LLM.Temperature != 0.6 {
		t.Fatalf("expected temperature 0.6 from preset, got %f", session.LLM.Temperature)
	}
	if base.LLM.Temperature == 0.6 {
		t.Fatal("preset should not mutate base config")
	}
}

func TestForSessionAppliesModelPresetAfterSessionOverride(t *testing.T) {
	base := validConfig()
	base.LLM.Model = "gpt-4o"
	model := "Qwen3-32B"
	temp := 0.6
	base.Models = map[string]ModelConfig{
		"Qwen3-32B": {
			Temperature: &temp,
		},
	}
	base.Sessions = map[string]SessionOverride{
		"oc_test": {
			LLM: &LLMOverride{Model: &model},
		},
	}

	session := base.ForSession("oc_test")
	if session.LLM.Model != "Qwen3-32B" {
		t.Fatalf("expected model Qwen3-32B, got %s", session.LLM.Model)
	}
	if session.LLM.Temperature != 0.6 {
		t.Fatalf("expected temperature 0.6 from preset after session override, got %f", session.LLM.Temperature)
	}
}

func TestForSessionExtraBodyOverride(t *testing.T) {
	base := validConfig()
	base.LLM.ExtraBody = map[string]any{"presence_penalty": 0.5}
	sessionExtra := map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": true}}
	base.Sessions = map[string]SessionOverride{
		"oc_test": {LLM: &LLMOverride{ExtraBody: sessionExtra}},
	}
	session := base.ForSession("oc_test")
	if session.LLM.ExtraBody["chat_template_kwargs"] == nil {
		t.Fatal("expected session extra_body to have chat_template_kwargs")
	}
	if _, ok := session.LLM.ExtraBody["presence_penalty"]; ok {
		t.Fatal("expected session extra_body to fully replace, not merge")
	}
}
