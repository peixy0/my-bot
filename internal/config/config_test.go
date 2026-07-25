package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() *Config {
	cfg := defaultConfig()
	cfg.LLM.APIKey = "key"
	cfg.LLM.TopP = 1
	cfg.Heartbeat.IntervalSeconds = 60
	cfg.Context.MaxImageBytes = 1024
	return cfg
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
		{"limiter rpm", func(c *Config) { c.Limiter = &LimiterConfig{RPM: 0, Burst: 1} }},
		{"limiter burst", func(c *Config) { c.Limiter = &LimiterConfig{RPM: 1, Burst: 0} }},
		{"webui host", func(c *Config) { c.WebUI.Host = "" }},
		{"webui port", func(c *Config) { c.WebUI.Port = 70000 }},
		{"browser bad address", func(c *Config) {
			c.Browser = &BrowserConfig{Enabled: true, ListenAddr: "bad", Path: "/browser"}
		}},
		{"browser bad path", func(c *Config) {
			c.Browser = &BrowserConfig{Enabled: true, ListenAddr: "127.0.0.1:8020", Path: "browser"}
		}},
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
	path := writeTestConfig(t, `
llm:
  api_key: key
`)
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
browser:
  enabled: true
  listen_addr: "127.0.0.1:8020"
  path: "/browser"
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
	if cfg.Browser == nil || !cfg.Browser.Enabled {
		t.Fatal("expected browser config to be parsed")
	}
}

func TestForSessionNoOverride(t *testing.T) {
	base := validConfig()
	session := base.ForSession("oc_missing")
	if session.LLM.APIKey != base.LLM.APIKey {
		t.Fatalf("expected same api_key, got %q", session.LLM.APIKey)
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

func TestContextOverrideApplyTo(t *testing.T) {
	target := ContextConfig{
		MaxImageBytes:        1024,
		MaxOutputTokens:      16384,
		CompressionThreshold: 0.7,
	}

	t.Run("all fields set", func(t *testing.T) {
		tgt := target
		maxImage := 2048
		maxTokens := int64(8192)
		threshold := 0.9
		override := ContextOverride{
			MaxImageBytes:        &maxImage,
			MaxOutputTokens:      &maxTokens,
			CompressionThreshold: &threshold,
		}
		override.ApplyTo(&tgt)
		if tgt.MaxImageBytes != 2048 {
			t.Fatalf("expected max_image_bytes 2048, got %d", tgt.MaxImageBytes)
		}
		if tgt.MaxOutputTokens != 8192 {
			t.Fatalf("expected max_output_tokens 8192, got %d", tgt.MaxOutputTokens)
		}
		if tgt.CompressionThreshold != 0.9 {
			t.Fatalf("expected compression_threshold 0.9, got %f", tgt.CompressionThreshold)
		}
	})

	t.Run("nil fields do not override", func(t *testing.T) {
		tgt := target
		override := ContextOverride{}
		override.ApplyTo(&tgt)
		if tgt.MaxImageBytes != 1024 {
			t.Fatalf("expected max_image_bytes unchanged 1024, got %d", tgt.MaxImageBytes)
		}
		if tgt.MaxOutputTokens != 16384 {
			t.Fatalf("expected max_output_tokens unchanged 16384, got %d", tgt.MaxOutputTokens)
		}
		if tgt.CompressionThreshold != 0.7 {
			t.Fatalf("expected compression_threshold unchanged 0.7, got %f", tgt.CompressionThreshold)
		}
	})
}

func TestBaseLLM(t *testing.T) {
	t.Run("returns pre-preset value after model preset applied", func(t *testing.T) {
		base := validConfig()
		base.LLM.Model = "Qwen3-32B"
		base.LLM.Temperature = 0.8
		presetTemp := 0.6
		base.Models = map[string]ModelConfig{
			"Qwen3-32B": {
				Temperature: &presetTemp,
			},
		}

		session := base.ForSession("oc_no_override")
		// ForSession applies the model preset onto merged.LLM, but baseLLM
		// captures the pre-preset value.
		if session.LLM.Temperature != 0.6 {
			t.Fatalf("expected session llm temperature 0.6 from preset, got %f", session.LLM.Temperature)
		}
		if got := session.BaseLLM(); got.Temperature != 0.8 {
			t.Fatalf("expected base llm temperature 0.8, got %f", got.Temperature)
		}
	})

	t.Run("fallback when baseLLM is nil", func(t *testing.T) {
		cfg := defaultConfig()
		cfg.LLM.APIKey = "key"
		// A fresh Config without ForSession has baseLLM == nil, so BaseLLM
		// returns the LLM field directly.
		if got := cfg.BaseLLM(); got.Temperature != cfg.LLM.Temperature {
			t.Fatalf("expected base llm temperature %f, got %f", cfg.LLM.Temperature, got.Temperature)
		}
		if got := cfg.BaseLLM(); got.APIKey != cfg.LLM.APIKey {
			t.Fatalf("expected base llm api_key %q, got %q", cfg.LLM.APIKey, got.APIKey)
		}
	})
}

func TestLoadNonexistentFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err == nil {
		t.Fatal("expected error loading nonexistent file")
	}
	if !strings.Contains(err.Error(), "read config") {
		t.Fatalf("expected error containing \"read config\", got %v", err)
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	path := writeTestConfig(t, "llm: [unclosed")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error loading malformed yaml")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("expected error containing \"parse config\", got %v", err)
	}
}

func TestLoadValidationFailure(t *testing.T) {
	path := writeTestConfig(t, "llm:\n  api_key: \"\"")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error loading config with empty api_key")
	}
	if !strings.Contains(err.Error(), "api_key is required") {
		t.Fatalf("expected error containing \"api_key is required\", got %v", err)
	}
}
