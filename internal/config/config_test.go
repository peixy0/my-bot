package config

import "testing"

func validConfig() *Config {
	return &Config{
		OpenAIAPIKey:                "key",
		ContainerRuntime:            "",
		MaxOutputChars:              1000,
		WakeIntervalSeconds:         60,
		ContextMaxTokens:            32000,
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
		{"container runtime", func(c *Config) { c.ContainerRuntime = "runc" }},
		{"max output", func(c *Config) { c.MaxOutputChars = 0 }},
		{"wake interval", func(c *Config) { c.WakeIntervalSeconds = -1 }},
		{"context max tokens", func(c *Config) { c.ContextMaxTokens = 0 }},
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
