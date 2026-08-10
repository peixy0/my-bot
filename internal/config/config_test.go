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
	cfg.LLM.TopP = floatPtr(1)
	cfg.Heartbeat.IntervalSeconds = 60
	cfg.Context.MaxImageBytes = 1024
	return cfg
}

func strPtr(s string) *string     { return &s }
func floatPtr(f float64) *float64 { return &f }
func intPtr(i int) *int           { return &i }
func int64Ptr(i int64) *int64     { return &i }
func boolPtr(b bool) *bool        { return &b }

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
		{"top p low", func(c *Config) { c.LLM.TopP = floatPtr(-0.1) }},
		{"top p high", func(c *Config) { c.LLM.TopP = floatPtr(1.1) }},
		{"top k", func(c *Config) { c.LLM.TopK = intPtr(-1) }},
		{"container enabled no name", func(c *Config) { c.Container = &ContainerConfig{Runtime: "podman"} }},
		{"container enabled bad runtime", func(c *Config) { c.Container = &ContainerConfig{Name: "x", Runtime: "runc"} }},
		{"tool max output chars", func(c *Config) { c.Tool.MaxOutputChars = 0 }},
		{"heartbeat interval", func(c *Config) { c.Heartbeat.IntervalSeconds = -1 }},
		{"llm context window", func(c *Config) { c.LLM.ContextWindow = 0 }},
		{"max output tokens", func(c *Config) { c.Context.MaxOutputTokens = 0 }},
		{"compression threshold low", func(c *Config) { c.Context.CompressionThreshold = 0 }},
		{"compression threshold high", func(c *Config) { c.Context.CompressionThreshold = 1.1 }},
		{"compression tool result truncate negative", func(c *Config) { c.Context.CompressionToolResultTruncate = -1 }},
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
		{"preset missing name", func(c *Config) {
			c.Presets = map[string]Preset{"": {Temperature: floatPtr(0.5)}}
		}},
		{"preset temperature", func(c *Config) {
			c.Presets = map[string]Preset{"p": {Temperature: floatPtr(2.1)}}
		}},
		{"preset top p", func(c *Config) {
			c.Presets = map[string]Preset{"p": {TopP: floatPtr(1.1)}}
		}},
		{"preset top k", func(c *Config) {
			c.Presets = map[string]Preset{"p": {TopK: intPtr(-1)}}
		}},
		{"preset context window", func(c *Config) {
			c.Presets = map[string]Preset{"p": {ContextWindow: int64Ptr(0)}}
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
	if cfg.Context.CompressionToolResultTruncate != 2000 {
		t.Fatalf("expected default compression_tool_result_truncate 2000, got %d", cfg.Context.CompressionToolResultTruncate)
	}
	if cfg.Tool.MaxOutputChars != 100000 {
		t.Fatalf("expected default max_output_chars 100000, got %d", cfg.Tool.MaxOutputChars)
	}
	if cfg.LLM.TopP != nil || cfg.LLM.TopK != nil {
		t.Fatalf("expected top_p and top_k to be unset by default, got top_p=%v top_k=%v", cfg.LLM.TopP, cfg.LLM.TopK)
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

func TestLoadYAMLPresets(t *testing.T) {
	path := writeTestConfig(t, `
llm:
  api_key: key
context:
  compression_model: "my-compression"
presets:
  my-completion:
    model: "gpt-4o"
    temperature: 0.7
  my-compression:
    model: "gpt-4o-mini"
    temperature: 0.2
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(cfg.Presets) != 2 {
		t.Fatalf("expected 2 presets, got %d", len(cfg.Presets))
	}
	if cfg.Context.CompressionModel != "my-compression" {
		t.Fatalf("expected compression_model my-compression, got %s", cfg.Context.CompressionModel)
	}
}

func TestForSessionNoOverride(t *testing.T) {
	base := validConfig()
	session := base.ForSession("oc_missing")
	if session.LLM.APIKey != base.LLM.APIKey {
		t.Fatalf("expected same api_key, got %q", session.LLM.APIKey)
	}
}

func TestForSessionSessionOverrideCompletionPreset(t *testing.T) {
	base := validConfig()
	base.Presets = map[string]Preset{
		"global-completion":  {Model: strPtr("gpt-4o"), Temperature: floatPtr(1.0)},
		"session-completion": {Model: strPtr("gpt-4o-mini"), Temperature: floatPtr(0.3)},
	}
	base.Sessions = map[string]SessionOverride{
		"oc_test": {
			Model: "session-completion",
		},
	}

	session := base.ForSession("oc_test")
	if session.LLM.Model != "gpt-4o-mini" {
		t.Fatalf("expected model gpt-4o-mini, got %s", session.LLM.Model)
	}
	if session.LLM.Temperature != 0.3 {
		t.Fatalf("expected temperature 0.3 from session preset, got %f", session.LLM.Temperature)
	}
	if session.Sessions != nil {
		t.Fatal("expected sessions to be nil in merged config")
	}
}

func TestForSessionSessionOverrideCompressionPreset(t *testing.T) {
	base := validConfig()
	base.Context.CompressionModel = "global-compression"
	base.Presets = map[string]Preset{
		"global-compression":  {Model: strPtr("gpt-4o"), Temperature: floatPtr(0.5)},
		"session-compression": {Model: strPtr("gpt-4o-mini"), Temperature: floatPtr(0.1)},
	}
	base.Sessions = map[string]SessionOverride{
		"oc_test": {
			CompressionModel: "session-compression",
		},
	}

	session := base.ForSession("oc_test")
	if session.Context.CompressionModel != "session-compression" {
		t.Fatalf("expected compression_model session-compression, got %s", session.Context.CompressionModel)
	}
}

func TestForSessionDoesNotMutateBase(t *testing.T) {
	base := validConfig()
	base.Sessions = map[string]SessionOverride{
		"oc_test": {
			Model: "some-preset",
		},
	}

	_ = base.ForSession("oc_test")
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

func TestPresetApplyTo(t *testing.T) {
	target := LLMConfig{
		Temperature: 1.0,
		TopP:        floatPtr(0.95),
		TopK:        intPtr(0),
		ExtraBody:   map[string]any{"old": true},
	}

	t.Run("all fields set", func(t *testing.T) {
		tgt := target
		preset := Preset{
			Temperature: floatPtr(0.6),
			TopP:        floatPtr(0.8),
			TopK:        intPtr(50),
			ExtraBody:   map[string]any{"new": true},
		}
		preset.ApplyTo(&tgt)
		if tgt.Temperature != 0.6 {
			t.Fatalf("expected temperature 0.6, got %f", tgt.Temperature)
		}
		if tgt.TopP == nil || *tgt.TopP != 0.8 {
			t.Fatalf("expected top_p 0.8, got %v", tgt.TopP)
		}
		if tgt.TopK == nil || *tgt.TopK != 50 {
			t.Fatalf("expected top_k 50, got %v", tgt.TopK)
		}
		if tgt.ExtraBody["new"] == nil {
			t.Fatal("expected extra_body to be replaced")
		}
		if _, ok := tgt.ExtraBody["old"]; ok {
			t.Fatal("expected extra_body to be fully replaced, not merged")
		}
	})

	t.Run("zero fields do not override", func(t *testing.T) {
		tgt := target
		preset := Preset{}
		preset.ApplyTo(&tgt)
		if tgt.Temperature != 1.0 {
			t.Fatalf("expected temperature unchanged 1.0, got %f", tgt.Temperature)
		}
		if tgt.TopP == nil || *tgt.TopP != 0.95 {
			t.Fatalf("expected top_p unchanged 0.95, got %v", tgt.TopP)
		}
		if tgt.TopK == nil || *tgt.TopK != 0 {
			t.Fatalf("expected top_k unchanged 0, got %v", tgt.TopK)
		}
		if tgt.ExtraBody["old"] == nil {
			t.Fatal("expected extra_body unchanged")
		}
	})
}

func TestForSessionWithGlobalCompletionPreset(t *testing.T) {
	base := validConfig()
	base.LLM.Model = "gpt-4o"
	base.LLM.Temperature = 1.0
	base.Presets = map[string]Preset{
		"qwen": {Model: strPtr("Qwen3-32B"), Temperature: floatPtr(0.6)},
	}
	base.Sessions = map[string]SessionOverride{
		"oc_test": {
			Model: "qwen",
		},
	}

	session := base.ForSession("oc_test")
	if session.LLM.Model != "Qwen3-32B" {
		t.Fatalf("expected model Qwen3-32B, got %s", session.LLM.Model)
	}
	if session.LLM.Temperature != 0.6 {
		t.Fatalf("expected temperature 0.6 from preset, got %f", session.LLM.Temperature)
	}
}

func TestCompletionPresetFallbackToModelName(t *testing.T) {
	base := validConfig()
	// no preset, no session override - model stays as is
	session := base.ForSession("oc_no_override")
	if session.LLM.Model != "gpt-4o" {
		t.Fatalf("expected model gpt-4o, got %s", session.LLM.Model)
	}
}

func TestSessionCompletionPresetFallbackToModelName(t *testing.T) {
	base := validConfig()
	base.Presets = map[string]Preset{
		"some-preset": {Temperature: floatPtr(0.5)},
	}
	base.Sessions = map[string]SessionOverride{
		"oc_test": {
			Model: "gpt-4o-mini", // not a preset
		},
	}

	session := base.ForSession("oc_test")
	if session.LLM.Model != "gpt-4o-mini" {
		t.Fatalf("expected model gpt-4o-mini (fallback), got %s", session.LLM.Model)
	}
}

func TestValidateAllowsDirectSessionAndCompressionModels(t *testing.T) {
	base := validConfig()
	base.Context.CompressionModel = "gpt-4o-mini"
	base.Sessions = map[string]SessionOverride{
		"oc_test": {
			Model:            "gpt-4o-mini",
			CompressionModel: "gpt-4o-nano",
		},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("expected direct model names to be valid: %v", err)
	}
}

func TestCompressionLLMConfig(t *testing.T) {
	t.Run("no preset returns base LLMConfig", func(t *testing.T) {
		base := validConfig()
		base.LLM.Model = "gpt-4o"
		base.LLM.Temperature = 1.0
		base.LLM.TopP = floatPtr(0.95)

		comp := base.CompressionLLMConfig()
		if comp.Model != "gpt-4o" {
			t.Fatalf("expected model gpt-4o, got %s", comp.Model)
		}
		if comp.Temperature != 1.0 {
			t.Fatalf("expected temperature 1.0, got %f", comp.Temperature)
		}
		if comp.TopP == nil || *comp.TopP != 0.95 {
			t.Fatalf("expected top_p 0.95, got %v", comp.TopP)
		}
	})

	t.Run("applies global compression preset", func(t *testing.T) {
		base := validConfig()
		base.LLM.Model = "gpt-4o"
		base.LLM.Temperature = 1.0
		base.Context.CompressionModel = "qwen-compression"
		base.Presets = map[string]Preset{
			"qwen-compression": {Model: strPtr("Qwen3-32B"), Temperature: floatPtr(0.2)},
		}

		comp := base.CompressionLLMConfig()
		if comp.Model != "Qwen3-32B" {
			t.Fatalf("expected model Qwen3-32B, got %s", comp.Model)
		}
		if comp.Temperature != 0.2 {
			t.Fatalf("expected temperature 0.2 from preset, got %f", comp.Temperature)
		}
	})

	t.Run("session compression preset merged by ForSession", func(t *testing.T) {
		base := validConfig()
		base.LLM.Model = "gpt-4o"
		base.LLM.Temperature = 1.0
		base.Context.CompressionModel = "global-compression"
		base.Presets = map[string]Preset{
			"global-compression":  {Temperature: floatPtr(0.5)},
			"session-compression": {Temperature: floatPtr(0.1)},
		}
		base.Sessions = map[string]SessionOverride{
			"oc_test": {
				CompressionModel: "session-compression",
			},
		}

		session := base.ForSession("oc_test")
		comp := session.CompressionLLMConfig()

		if comp.Temperature != 0.1 {
			t.Fatalf("expected temperature 0.1 from session preset, got %f", comp.Temperature)
		}
	})

	t.Run("fallback to model name when preset not found", func(t *testing.T) {
		base := validConfig()
		base.LLM.Model = "gpt-4o"
		base.LLM.Temperature = 1.0
		base.Context.CompressionModel = "gpt-4o-mini" // not a preset

		comp := base.CompressionLLMConfig()
		if comp.Model != "gpt-4o-mini" {
			t.Fatalf("expected model gpt-4o-mini (fallback), got %s", comp.Model)
		}
		if comp.Temperature != 1.0 {
			t.Fatalf("expected temperature unchanged 1.0, got %f", comp.Temperature)
		}
	})
}

func TestFindPreset(t *testing.T) {
	base := validConfig()
	base.Presets = map[string]Preset{
		"qwen": {Model: strPtr("Qwen3-32B"), Temperature: floatPtr(0.6)},
	}

	p := base.FindPreset("qwen")
	if p == nil {
		t.Fatal("expected to find preset qwen")
	}
	if p.Model == nil || *p.Model != "Qwen3-32B" {
		t.Fatalf("expected model Qwen3-32B, got %v", p.Model)
	}
	if base.FindPreset("missing") != nil {
		t.Fatal("expected nil for missing preset")
	}
}

func TestLoadNonexistentFile(t *testing.T) {
	_, err := Load("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	path := writeTestConfig(t, `llm: [}`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for malformed yaml")
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("expected parse config error, got %v", err)
	}
}

func TestLoadValidationFailure(t *testing.T) {
	path := writeTestConfig(t, `
llm:
  api_key: ""
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "llm.api_key is required") {
		t.Fatalf("expected api_key error, got %v", err)
	}
}
