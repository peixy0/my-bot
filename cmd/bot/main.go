package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"my-bot/internal/api"
	"my-bot/internal/config"
	"my-bot/internal/engine"
	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/llm"
	"my-bot/internal/messaging/feishu"
	"my-bot/internal/runtime"
	"my-bot/internal/tools"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		slog.Warn("invalid LOG_LEVEL, defaulting to info", "value", cfg.LogLevel)
		logLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	if err := os.MkdirAll(cfg.CWD, 0755); err != nil {
		slog.Error("create workspace", "err", err)
		os.Exit(1)
	}
	if err := os.Chdir(cfg.CWD); err != nil {
		slog.Error("chdir", "err", err)
		os.Exit(1)
	}

	rt := buildRuntime(cfg)
	skills := tools.NewSkillLoader(cfg.SkillsDir)

	llmClient := buildLLMClient(cfg)
	agent := llm.NewAgent(llmClient)

	mainInbox := inbox.NewMemory[events.AgentEvent](256)

	if cfg.FeishuAppID != "" {
		feishuCfg := feishu.Config{
			AppID:             cfg.FeishuAppID,
			AppSecret:         cfg.FeishuAppSecret,
			EncryptKey:        cfg.FeishuEncryptKey,
			VerificationToken: cfg.FeishuVerificationToken,
		}
		inbound := feishu.NewInbound(feishuCfg, mainInbox, rt)
		go func() {
			if err := inbound.Run(ctx); err != nil && err != context.Canceled {
				slog.Error("feishu inbound", "err", err)
			}
		}()
	}

	if cfg.WebUIEnabled {
		srv := api.NewServer(mainInbox, cfg.ProjectDir, rt, api.ServerOptions{
			Token: cfg.WebUIToken,
		})
		addr := fmt.Sprintf("%s:%d", cfg.WebUIHost, cfg.WebUIPort)
		go func() {
			slog.Info("WebUI listening", "addr", "http://"+addr)
			if err := srv.Run(ctx, addr); err != nil {
				slog.Error("api server", "err", err)
			}
		}()
	}

	cronLoader := engine.NewCronLoader(cfg.CronsDir)
	scheduler := engine.NewScheduler(cfg, agent, rt, skills, mainInbox, cronLoader)

	slog.Info("bot started", "model", cfg.OpenAIModel)
	if err := scheduler.Run(ctx); err != nil && err != context.Canceled {
		slog.Error("scheduler", "err", err)
		os.Exit(1)
	}
}

func buildRuntime(cfg *config.Config) runtime.Runtime {
	if cfg.ContainerName != "" && cfg.ContainerRuntime != "" {
		bin := cfg.ContainerRuntime
		rt, err := runtime.NewContainerRuntime(cfg.ContainerName, bin, "/workspace", cfg.ToolMaxOutputChars)
		if err != nil {
			slog.Error("container runtime unavailable", "err", err)
			os.Exit(1)
		}
		slog.Info("using container runtime", "container", cfg.ContainerName)
		return rt
	}
	slog.Info("using host runtime")
	return runtime.NewHostRuntime(cfg.ToolMaxOutputChars)
}

func buildLLMClient(cfg *config.Config) llm.CompletionClient {
	return llm.NewOpenAIProvider(cfg.OpenAIBaseURL, cfg.OpenAIAPIKey)
}
