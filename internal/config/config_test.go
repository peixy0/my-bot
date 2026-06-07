package config

import "testing"

func validConfig() *Config {
	return &Config{
		OpenAIAPIKey:                "key",
		Temperature:                 1,
		TopP:                        1,
		ContainerRuntime:            "",
		ToolMaxOutputChars:          1000,
		WakeIntervalSeconds:         60,
		ContextWindowTokens:         128000,
		MaxOutputTokens:             16384,
		ContextCompressionThreshold: 0.7,
		MaxImageSizeBytes:           1024,
		WebUIEnabled:                true,
		WebUIHost:                   "127.0.0.1",
		WebUIPort:                   8017,
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
		{"openai key", func(c *Config) { c.OpenAIAPIKey = "" }},
		{"temperature low", func(c *Config) { c.Temperature = -0.1 }},
		{"temperature high", func(c *Config) { c.Temperature = 2.1 }},
		{"top p low", func(c *Config) { c.TopP = -0.1 }},
		{"top p high", func(c *Config) { c.TopP = 1.1 }},
		{"top k", func(c *Config) { c.TopK = -1 }},
		{"container runtime", func(c *Config) { c.ContainerRuntime = "runc" }},
		{"tool max output chars", func(c *Config) { c.ToolMaxOutputChars = 0 }},
		{"wake interval", func(c *Config) { c.WakeIntervalSeconds = -1 }},
		{"context window tokens", func(c *Config) { c.ContextWindowTokens = 0 }},
		{"max output tokens", func(c *Config) { c.MaxOutputTokens = 0 }},
		{"compression threshold low", func(c *Config) { c.ContextCompressionThreshold = 0 }},
		{"compression threshold high", func(c *Config) { c.ContextCompressionThreshold = 1.1 }},
		{"image size", func(c *Config) { c.MaxImageSizeBytes = 0 }},
		{"webui host", func(c *Config) { c.WebUIHost = "" }},
		{"webui port", func(c *Config) { c.WebUIPort = 70000 }},
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

func TestLoadTokenDefaults(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ContextWindowTokens != 128000 {
		t.Fatalf("expected default context window tokens 128000, got %d", cfg.ContextWindowTokens)
	}
	if cfg.MaxOutputTokens != 16384 {
		t.Fatalf("expected default max output tokens 16384, got %d", cfg.MaxOutputTokens)
	}
	if cfg.ToolMaxOutputChars != 100000 {
		t.Fatalf("expected default tool max output chars 100000, got %d", cfg.ToolMaxOutputChars)
	}
}

func TestLoadTokenConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("CONTEXT_WINDOW_TOKENS", "128000")
	t.Setenv("MAX_OUTPUT_TOKENS", "8192")
	t.Setenv("TOOL_MAX_OUTPUT_CHARS", "50000")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ContextWindowTokens != 128000 {
		t.Fatalf("expected CONTEXT_WINDOW_TOKENS to win, got %d", cfg.ContextWindowTokens)
	}
	if cfg.MaxOutputTokens != 8192 {
		t.Fatalf("expected max output tokens 8192, got %d", cfg.MaxOutputTokens)
	}
	if cfg.ToolMaxOutputChars != 50000 {
		t.Fatalf("expected tool max output chars 50000, got %d", cfg.ToolMaxOutputChars)
	}
}
