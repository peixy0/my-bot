package config

import (
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type ModelConfig struct {
	Temperature             *float64       `yaml:"temperature,omitempty"`
	TopP                    *float64       `yaml:"top_p,omitempty"`
	TopK                    *int           `yaml:"top_k,omitempty"`
	ContextWindow           *int64         `yaml:"context_window,omitempty"`
	Vision                  *bool          `yaml:"vision,omitempty"`
	SkipOnToolDispatchError *bool          `yaml:"skip_on_tool_dispatch_error,omitempty"`
	ExtraBody               map[string]any `yaml:"extra_body,omitempty"`
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
	if m.ContextWindow != nil {
		target.ContextWindow = *m.ContextWindow
	}
	if m.Vision != nil {
		target.Vision = *m.Vision
	}
	if m.SkipOnToolDispatchError != nil {
		target.SkipOnToolDispatchError = *m.SkipOnToolDispatchError
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
	Browser   *BrowserConfig             `yaml:"browser,omitempty"`
	Tool      ToolConfig                 `yaml:"tool"`
	Workspace WorkspaceConfig            `yaml:"workspace"`
	Context   ContextConfig              `yaml:"context"`
	Feishu    *FeishuConfig              `yaml:"feishu"`
	WeChat    *WeChatConfig              `yaml:"wechat"`
	WebUI     WebUIConfig                `yaml:"webui"`
	Heartbeat HeartbeatConfig            `yaml:"heartbeat"`
	Sessions  map[string]SessionOverride `yaml:"sessions"`
	baseLLM   *LLMConfig
}

func (c *Config) BaseLLM() LLMConfig {
	if c.baseLLM != nil {
		return *c.baseLLM
	}
	return c.LLM
}

type LimiterConfig struct {
	RPM   int
	Burst int
}

type LLMConfig struct {
	BaseURL                 string         `yaml:"base_url"`
	Model                   string         `yaml:"model"`
	APIKey                  string         `yaml:"api_key"`
	Temperature             float64        `yaml:"temperature"`
	TopP                    float64        `yaml:"top_p"`
	TopK                    int            `yaml:"top_k"`
	ContextWindow           int64          `yaml:"context_window"`
	Vision                  bool           `yaml:"vision"`
	SkipOnToolDispatchError bool           `yaml:"skip_on_tool_dispatch_error"`
	ExtraBody               map[string]any `yaml:"extra_body"`
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

type BrowserConfig struct {
	Enabled    bool   `yaml:"enabled"`
	ListenAddr string `yaml:"listen_addr"`
	Path       string `yaml:"path"`
}

type WorkspaceConfig struct {
	CWD        string `yaml:"cwd"`
	SkillsDir  string `yaml:"skills_dir"`
	CronsDir   string `yaml:"crons_dir"`
	SessionDir string `yaml:"session_dir"`
}

type ContextConfig struct {
	MaxImageBytes        int     `yaml:"max_image_bytes"`
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

type WebUIConfig struct {
	Enabled       bool   `yaml:"enabled"`
	Host          string `yaml:"host"`
	Port          int    `yaml:"port"`
	Token         string `yaml:"token"`
	IndexHTMLPath string `yaml:"index_html_path"`
}

type HeartbeatConfig struct {
	IntervalSeconds int `yaml:"interval_seconds"`
}

type SessionOverride struct {
	LLM     *LLMOverride     `yaml:"llm,omitempty"`
	Context *ContextOverride `yaml:"context,omitempty"`
}

type LLMOverride struct {
	BaseURL                 *string        `yaml:"base_url,omitempty"`
	Model                   *string        `yaml:"model,omitempty"`
	APIKey                  *string        `yaml:"api_key,omitempty"`
	Temperature             *float64       `yaml:"temperature,omitempty"`
	TopP                    *float64       `yaml:"top_p,omitempty"`
	TopK                    *int           `yaml:"top_k,omitempty"`
	ContextWindow           *int64         `yaml:"context_window,omitempty"`
	Vision                  *bool          `yaml:"vision,omitempty"`
	SkipOnToolDispatchError *bool          `yaml:"skip_on_tool_dispatch_error,omitempty"`
	ExtraBody               map[string]any `yaml:"extra_body,omitempty"`
}

type ContextOverride struct {
	MaxImageBytes        *int     `yaml:"max_image_bytes,omitempty"`
	MaxOutputTokens      *int64   `yaml:"max_output_tokens,omitempty"`
	CompressionThreshold *float64 `yaml:"compression_threshold,omitempty"`
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
	if o.ContextWindow != nil {
		target.ContextWindow = *o.ContextWindow
	}
	if o.Vision != nil {
		target.Vision = *o.Vision
	}
	if o.SkipOnToolDispatchError != nil {
		target.SkipOnToolDispatchError = *o.SkipOnToolDispatchError
	}
	if o.ExtraBody != nil {
		target.ExtraBody = o.ExtraBody
	}
}

func (o *ContextOverride) ApplyTo(target *ContextConfig) {
	if o.MaxImageBytes != nil {
		target.MaxImageBytes = *o.MaxImageBytes
	}
	if o.MaxOutputTokens != nil {
		target.MaxOutputTokens = *o.MaxOutputTokens
	}
	if o.CompressionThreshold != nil {
		target.CompressionThreshold = *o.CompressionThreshold
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
	}

	base := merged.LLM
	merged.baseLLM = &base

	if preset, ok := merged.Models[merged.LLM.Model]; ok {
		preset.ApplyTo(&merged.LLM)
	}

	return &merged
}

func defaultConfig() *Config {
	return &Config{
		LogLevel: "debug",
		Limiter:  nil,
		Tool: ToolConfig{
			MaxOutputChars: 100_000,
		},
		Workspace: WorkspaceConfig{
			CWD:        "./workspace",
			SkillsDir:  "./.skills",
			CronsDir:   "./.cron",
			SessionDir: "./.session",
		},
		Heartbeat: HeartbeatConfig{
			IntervalSeconds: 1800,
		},
		LLM: LLMConfig{
			BaseURL:       "https://api.openai.com/v1",
			Model:         "gpt-4o",
			Temperature:   1,
			TopP:          0.95,
			ContextWindow: 128_000,
			Vision:        false,
		},
		Context: ContextConfig{
			MaxImageBytes:        5 * 1024 * 1024,
			MaxOutputTokens:      16_384,
			CompressionThreshold: 0.7,
		},
		WebUI: WebUIConfig{
			Enabled:       true,
			Host:          "127.0.0.1",
			Port:          8017,
			IndexHTMLPath: "../chat.html",
		},
	}
}

func (c *Config) Validate() error {
	if c.LLM.APIKey == "" {
		return fmt.Errorf("llm.api_key is required")
	}
	if c.LLM.Temperature < 0 || c.LLM.Temperature > 2 {
		return fmt.Errorf("llm.temperature must be between 0 and 2")
	}
	if c.LLM.TopP < 0 || c.LLM.TopP > 1 {
		return fmt.Errorf("llm.top_p must be between 0 and 1")
	}
	if c.LLM.TopK < 0 {
		return fmt.Errorf("llm.top_k must be non-negative")
	}
	if c.Limiter != nil {
		if c.Limiter.RPM <= 0 {
			return fmt.Errorf("limiter.rpm must be positive")
		}
		if c.Limiter.Burst <= 0 {
			return fmt.Errorf("limiter.burst must be positive")
		}
	}
	if c.Container != nil {
		switch c.Container.Runtime {
		case "podman", "docker":
		default:
			return fmt.Errorf("unsupported container.runtime %q", c.Container.Runtime)
		}
		if strings.TrimSpace(c.Container.Name) == "" {
			return fmt.Errorf("container.name is required when container is configured")
		}
	}
	if c.Tool.MaxOutputChars <= 0 {
		return fmt.Errorf("tool.max_output_chars must be positive")
	}
	if c.Browser != nil {
		browser := c.Browser
		if browser.Enabled {
			if strings.TrimSpace(browser.ListenAddr) == "" {
				return fmt.Errorf("browser.listen_addr is required")
			}
			if _, port, err := net.SplitHostPort(browser.ListenAddr); err != nil {
				return fmt.Errorf("parse browser.listen_addr: %w", err)
			} else if port == "0" {
				return fmt.Errorf("browser.listen_addr port must be non-zero")
			}
			if !strings.HasPrefix(browser.Path, "/") {
				return fmt.Errorf("browser.path must begin with /")
			}
		}
	}
	if c.Heartbeat.IntervalSeconds <= 0 {
		return fmt.Errorf("heartbeat.interval_seconds must be positive")
	}
	if c.LLM.ContextWindow <= 0 {
		return fmt.Errorf("llm.context_window must be positive")
	}
	if c.Context.MaxOutputTokens <= 0 {
		return fmt.Errorf("context.max_output_tokens must be positive")
	}
	if c.Context.CompressionThreshold <= 0 || c.Context.CompressionThreshold > 1 {
		return fmt.Errorf("context.compression_threshold must be greater than 0 and at most 1")
	}
	if c.Context.MaxImageBytes <= 0 {
		return fmt.Errorf("context.max_image_bytes must be positive")
	}
	if c.WebUI.Enabled {
		if strings.TrimSpace(c.WebUI.Host) == "" {
			return fmt.Errorf("webui.host is required when webui.enabled=true")
		}
		if c.WebUI.Port < 1 || c.WebUI.Port > 65535 {
			return fmt.Errorf("webui.port must be between 1 and 65535")
		}
	}
	return nil
}
