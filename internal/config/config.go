package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type ModelConfig struct {
	Temperature *float64       `yaml:"temperature,omitempty"`
	TopP        *float64       `yaml:"top_p,omitempty"`
	TopK        *int           `yaml:"top_k,omitempty"`
	ExtraBody   map[string]any `yaml:"extra_body,omitempty"`
}

func (m *ModelConfig) ApplyTo(target *LLMConfig) {
	if m.Temperature != nil {
		target.Temperature = *m.Temperature
	}
	if m.TopP != nil {
		target.TopP = *m.TopP
	}
	if m.TopK != nil {
		target.TopK = *m.TopK
	}
	if m.ExtraBody != nil {
		target.ExtraBody = m.ExtraBody
	}
}

type Config struct {
	LogLevel  string                     `yaml:"log_level"`
	LLM       LLMConfig                  `yaml:"llm"`
	Limiter   *LimiterConfig             `yaml:"limiter,omitempty"`
	Models    map[string]ModelConfig     `yaml:"models,omitempty"`
	Container *ContainerConfig           `yaml:"container,omitempty"`
	Tool      ToolConfig                 `yaml:"tool"`
	Workspace WorkspaceConfig            `yaml:"workspace"`
	Context   ContextConfig              `yaml:"context"`
	Feishu    *FeishuConfig              `yaml:"feishu"`
	WeChat    *WeChatConfig              `yaml:"wechat"`
	Vision    VisionConfig               `yaml:"vision"`
	WebUI     WebUIConfig                `yaml:"webui"`
	Heartbeat HeartbeatConfig            `yaml:"heartbeat"`
	Sessions  map[string]SessionOverride `yaml:"sessions"`
}

type LimiterConfig struct {
	RPM   int
	Burst int
}

type LLMConfig struct {
	BaseURL     string         `yaml:"base_url"`
	Model       string         `yaml:"model"`
	APIKey      string         `yaml:"api_key"`
	Temperature float64        `yaml:"temperature"`
	TopP        float64        `yaml:"top_p"`
	TopK        int            `yaml:"top_k"`
	ExtraBody   map[string]any `yaml:"extra_body"`
}

type ContainerConfig struct {
	Name    string `yaml:"name"`
	Runtime string `yaml:"runtime"`
}

type ToolConfig struct {
	MaxOutputChars int    `yaml:"max_output_chars"`
	WebSearchAPI   string `yaml:"web_search_api"`
	FetchProxy     string `yaml:"fetch_proxy"`
}

type WorkspaceConfig struct {
	CWD        string `yaml:"cwd"`
	ProjectDir string `yaml:"project_dir"`
	SkillsDir  string `yaml:"skills_dir"`
	CronsDir   string `yaml:"crons_dir"`
	SessionDir string `yaml:"session_dir"`
}

type ContextConfig struct {
	AutoCompression      bool    `yaml:"auto_compression"`
	WindowTokens         int64   `yaml:"window_tokens"`
	MaxOutputTokens      int64   `yaml:"max_output_tokens"`
	CompressionThreshold float64 `yaml:"compression_threshold"`
}

type FeishuConfig struct {
	AppID             string `yaml:"app_id"`
	AppSecret         string `yaml:"app_secret"`
	EncryptKey        string `yaml:"encrypt_key"`
	VerificationToken string `yaml:"verification_token"`
}

type WeChatConfig struct {
	BotToken string `yaml:"bot_token"`
	BaseURL  string `yaml:"base_url"`
}

type VisionConfig struct {
	Enabled       bool `yaml:"enabled"`
	MaxImageBytes int  `yaml:"max_image_bytes"`
}

type WebUIConfig struct {
	Enabled bool   `yaml:"enabled"`
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	Token   string `yaml:"token"`
}

type HeartbeatConfig struct {
	IntervalSeconds int `yaml:"interval_seconds"`
}

type SessionOverride struct {
	LLM     *LLMOverride     `yaml:"llm,omitempty"`
	Context *ContextOverride `yaml:"context,omitempty"`
	Vision  *VisionOverride  `yaml:"vision,omitempty"`
}

type LLMOverride struct {
	BaseURL     *string        `yaml:"base_url,omitempty"`
	Model       *string        `yaml:"model,omitempty"`
	APIKey      *string        `yaml:"api_key,omitempty"`
	Temperature *float64       `yaml:"temperature,omitempty"`
	TopP        *float64       `yaml:"top_p,omitempty"`
	TopK        *int           `yaml:"top_k,omitempty"`
	ExtraBody   map[string]any `yaml:"extra_body,omitempty"`
}

type ContextOverride struct {
	AutoCompression      *bool    `yaml:"auto_compression,omitempty"`
	WindowTokens         *int64   `yaml:"window_tokens,omitempty"`
	MaxOutputTokens      *int64   `yaml:"max_output_tokens,omitempty"`
	CompressionThreshold *float64 `yaml:"compression_threshold,omitempty"`
}

type VisionOverride struct {
	Enabled       *bool `yaml:"enabled,omitempty"`
	MaxImageBytes *int  `yaml:"max_image_bytes,omitempty"`
}

func (o *LLMOverride) ApplyTo(target *LLMConfig) {
	if o.BaseURL != nil {
		target.BaseURL = *o.BaseURL
	}
	if o.Model != nil {
		target.Model = *o.Model
	}
	if o.APIKey != nil {
		target.APIKey = *o.APIKey
	}
	if o.Temperature != nil {
		target.Temperature = *o.Temperature
	}
	if o.TopP != nil {
		target.TopP = *o.TopP
	}
	if o.TopK != nil {
		target.TopK = *o.TopK
	}
	if o.ExtraBody != nil {
		target.ExtraBody = o.ExtraBody
	}
}

func (o *ContextOverride) ApplyTo(target *ContextConfig) {
	if o.AutoCompression != nil {
		target.AutoCompression = *o.AutoCompression
	}
	if o.WindowTokens != nil {
		target.WindowTokens = *o.WindowTokens
	}
	if o.MaxOutputTokens != nil {
		target.MaxOutputTokens = *o.MaxOutputTokens
	}
	if o.CompressionThreshold != nil {
		target.CompressionThreshold = *o.CompressionThreshold
	}
}

func (o *VisionOverride) ApplyTo(target *VisionConfig) {
	if o.Enabled != nil {
		target.Enabled = *o.Enabled
	}
	if o.MaxImageBytes != nil {
		target.MaxImageBytes = *o.MaxImageBytes
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := defaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) ForSession(chatID string) *Config {
	merged := *c
	merged.Sessions = nil

	if override, ok := c.Sessions[chatID]; ok {
		if override.LLM != nil {
			override.LLM.ApplyTo(&merged.LLM)
		}
		if override.Context != nil {
			override.Context.ApplyTo(&merged.Context)
		}
		if override.Vision != nil {
			override.Vision.ApplyTo(&merged.Vision)
		}
	}

	if preset, ok := merged.Models[merged.LLM.Model]; ok {
		preset.ApplyTo(&merged.LLM)
	}

	return &merged
}

func defaultConfig() *Config {
	return &Config{
		LogLevel: "debug",
		LLM: LLMConfig{
			BaseURL:     "https://api.openai.com/v1",
			Model:       "gpt-4o",
			Temperature: 1,
			TopP:        0.95,
		},
		Limiter: nil,
		Tool: ToolConfig{
			MaxOutputChars: 100_000,
		},
		Workspace: WorkspaceConfig{
			CWD:        "./workspace",
			ProjectDir: "../",
			SkillsDir:  "./.skills",
			CronsDir:   "./.cron",
			SessionDir: "./.session",
		},
		Heartbeat: HeartbeatConfig{
			IntervalSeconds: 1800,
		},
		Context: ContextConfig{
			AutoCompression:      true,
			WindowTokens:         128_000,
			MaxOutputTokens:      16_384,
			CompressionThreshold: 0.7,
		},
		Vision: VisionConfig{
			MaxImageBytes: 5 * 1024 * 1024,
		},
		WebUI: WebUIConfig{
			Enabled: true,
			Host:    "127.0.0.1",
			Port:    8017,
		},
	}
}

func (cfg *Config) Validate() error {
	if cfg.LLM.APIKey == "" {
		return fmt.Errorf("llm.api_key is required")
	}
	if cfg.LLM.Temperature < 0 || cfg.LLM.Temperature > 2 {
		return fmt.Errorf("llm.temperature must be between 0 and 2")
	}
	if cfg.LLM.TopP < 0 || cfg.LLM.TopP > 1 {
		return fmt.Errorf("llm.top_p must be between 0 and 1")
	}
	if cfg.LLM.TopK < 0 {
		return fmt.Errorf("llm.top_k must be non-negative")
	}
	if cfg.Limiter != nil {
		if cfg.Limiter.RPM <= 0 {
			return fmt.Errorf("limiter.rpm must be positive")
		}
		if cfg.Limiter.Burst <= 0 {
			return fmt.Errorf("limiter.burst must be positive")
		}
	}
	if cfg.Container != nil {
		switch cfg.Container.Runtime {
		case "podman", "docker":
		default:
			return fmt.Errorf("unsupported container.runtime %q", cfg.Container.Runtime)
		}
		if strings.TrimSpace(cfg.Container.Name) == "" {
			return fmt.Errorf("container.name is required when container is configured")
		}
	}
	if cfg.Tool.MaxOutputChars <= 0 {
		return fmt.Errorf("tool.max_output_chars must be positive")
	}
	if cfg.Heartbeat.IntervalSeconds <= 0 {
		return fmt.Errorf("heartbeat.interval_seconds must be positive")
	}
	if cfg.Context.WindowTokens <= 0 {
		return fmt.Errorf("context.window_tokens must be positive")
	}
	if cfg.Context.MaxOutputTokens <= 0 {
		return fmt.Errorf("context.max_output_tokens must be positive")
	}
	if cfg.Context.CompressionThreshold <= 0 || cfg.Context.CompressionThreshold > 1 {
		return fmt.Errorf("context.compression_threshold must be greater than 0 and at most 1")
	}
	if cfg.Vision.MaxImageBytes <= 0 {
		return fmt.Errorf("vision.max_image_bytes must be positive")
	}
	if cfg.WebUI.Enabled {
		if strings.TrimSpace(cfg.WebUI.Host) == "" {
			return fmt.Errorf("webui.host is required when webui.enabled=true")
		}
		if cfg.WebUI.Port < 1 || cfg.WebUI.Port > 65535 {
			return fmt.Errorf("webui.port must be between 1 and 65535")
		}
	}
	return nil
}
