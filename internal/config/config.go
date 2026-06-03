package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	LogLevel string // "debug" | "info" | "warn" | "error"

	OpenAIBaseURL string
	OpenAIModel   string
	OpenAIAPIKey  string

	ContainerName    string
	ContainerRuntime string // "podman" | "docker" | "" (host)

	MaxOutputChars int
	WebSearchAPI   string
	FetchProxy     string

	CWD        string
	ProjectDir string
	SkillsDir  string
	CronsDir   string
	SessionDir string

	WakeIntervalSeconds int

	ContextAutoCompressionEnabled bool
	ContextMaxTokens              int
	ContextCompressionThreshold   float64

	FeishuAppID             string
	FeishuAppSecret         string
	FeishuEncryptKey        string
	FeishuVerificationToken string

	VisionSupport     bool
	MaxImageSizeBytes int

	WebUIEnabled bool
	WebUIHost    string
	WebUIPort    int
	WebUIToken   string
}

func Load() (*Config, error) {
	_ = godotenv.Load(".env")

	cfg := &Config{
		LogLevel: getEnv("LOG_LEVEL", "debug"),

		OpenAIBaseURL: getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAIModel:   getEnv("OPENAI_MODEL", "gpt-4o"),
		OpenAIAPIKey:  getEnv("OPENAI_API_KEY", ""),

		ContainerName:    getEnv("CONTAINER_NAME", ""),
		ContainerRuntime: getEnv("CONTAINER_RUNTIME", ""),

		MaxOutputChars: getEnvInt("MAX_OUTPUT_CHARS", 100_000),
		WebSearchAPI:   getEnv("WEB_SEARCH_API", ""),
		FetchProxy:     getEnv("FETCH_PROXY", ""),

		CWD:        getEnv("CWD", "./workspace"),
		ProjectDir: getEnv("PROJECT_DIR", "../"),
		SkillsDir:  getEnv("SKILLS_DIR", "./.skills"),
		CronsDir:   getEnv("CRONS_DIR", "./.cron"),
		SessionDir: getEnv("SESSION_DIR", "./.session"),

		WakeIntervalSeconds: getEnvInt("WAKE_INTERVAL_SECONDS", 1800),

		ContextAutoCompressionEnabled: getEnvBool("CONTEXT_AUTO_COMPRESSION_ENABLED", true),
		ContextMaxTokens:              getEnvInt("CONTEXT_MAX_TOKENS", 32_000),
		ContextCompressionThreshold:   getEnvFloat("CONTEXT_COMPRESSION_THRESHOLD", 0.7),

		FeishuAppID:             getEnv("FEISHU_APP_ID", ""),
		FeishuAppSecret:         getEnv("FEISHU_APP_SECRET", ""),
		FeishuEncryptKey:        getEnv("FEISHU_ENCRYPT_KEY", ""),
		FeishuVerificationToken: getEnv("FEISHU_VERIFICATION_TOKEN", ""),

		VisionSupport:     getEnvBool("VISION_SUPPORT", false),
		MaxImageSizeBytes: getEnvInt("MAX_IMAGE_SIZE_BYTES", 5*1024*1024),

		WebUIEnabled: getEnvBool("WEBUI_ENABLED", true),
		WebUIHost:    getEnv("WEBUI_HOST", "127.0.0.1"),
		WebUIPort:    getEnvInt("WEBUI_PORT", 8017),
		WebUIToken:   getEnv("WEBUI_TOKEN", ""),
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (cfg *Config) Validate() error {
	if cfg.OpenAIAPIKey == "" {
		return fmt.Errorf("OPENAI_API_KEY is required")
	}
	switch cfg.ContainerRuntime {
	case "", "podman", "docker":
	default:
		return fmt.Errorf("unsupported CONTAINER_RUNTIME %q", cfg.ContainerRuntime)
	}
	if cfg.MaxOutputChars <= 0 {
		return fmt.Errorf("MAX_OUTPUT_CHARS must be positive")
	}
	if cfg.WakeIntervalSeconds <= 0 {
		return fmt.Errorf("WAKE_INTERVAL_SECONDS must be positive")
	}
	if cfg.ContextMaxTokens <= 0 {
		return fmt.Errorf("CONTEXT_MAX_TOKENS must be positive")
	}
	if cfg.ContextCompressionThreshold <= 0 || cfg.ContextCompressionThreshold > 1 {
		return fmt.Errorf("CONTEXT_COMPRESSION_THRESHOLD must be greater than 0 and at most 1")
	}
	if cfg.MaxImageSizeBytes <= 0 {
		return fmt.Errorf("MAX_IMAGE_SIZE_BYTES must be positive")
	}
	if cfg.WebUIEnabled {
		if strings.TrimSpace(cfg.WebUIHost) == "" {
			return fmt.Errorf("WEBUI_HOST is required when WEBUI_ENABLED=true")
		}
		if cfg.WebUIPort < 1 || cfg.WebUIPort > 65535 {
			return fmt.Errorf("WEBUI_PORT must be between 1 and 65535")
		}
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
