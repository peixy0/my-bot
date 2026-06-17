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
	"my-bot/internal/messaging/wechat"
	"my-bot/internal/runtime"
	"my-bot/internal/tools"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	configPath := "config.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	var logLevel slog.Level
	if err := logLevel.UnmarshalText([]byte(cfg.LogLevel)); err != nil {
		slog.Warn("invalid log_level, defaulting to info", "value", cfg.LogLevel)
		logLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	if err := os.MkdirAll(cfg.Workspace.CWD, 0755); err != nil {
		slog.Error("create workspace", "err", err)
		os.Exit(1)
	}
	if err := os.Chdir(cfg.Workspace.CWD); err != nil {
		slog.Error("chdir", "err", err)
		os.Exit(1)
	}

	rt := buildRuntime(cfg)
	skills := tools.NewSkillLoader(cfg.Workspace.SkillsDir)

	llmClient := buildLLMClient(cfg)
	agent := llm.NewAgent(llmClient)

	mainInbox := inbox.NewMemory[events.AgentEvent](256)

	if cfg.Feishu.AppID != "" {
		feishuCfg := feishu.Config{
			AppID:             cfg.Feishu.AppID,
			AppSecret:         cfg.Feishu.AppSecret,
			EncryptKey:        cfg.Feishu.EncryptKey,
			VerificationToken: cfg.Feishu.VerificationToken,
		}
		inbound := feishu.NewInbound(feishuCfg, mainInbox, rt)
		go func() {
			if err := inbound.Run(ctx); err != nil && err != context.Canceled {
				slog.Error("feishu inbound", "err", err)
			}
		}()
	}

	if cfg.WeChat.Enabled {
		wcCfg := wechat.Config{
			BotToken: cfg.WeChat.BotToken,
			BaseURL:  cfg.WeChat.BaseURL,
		}
		wcInbound := wechat.NewInbound(wcCfg, mainInbox)
		go func() {
			if err := wcInbound.Run(ctx); err != nil && err != context.Canceled {
				slog.Error("wechat inbound", "err", err)
			}
		}()
	}

	if cfg.WebUI.Enabled {
		srv := api.NewServer(mainInbox, cfg.Workspace.ProjectDir, rt, api.ServerOptions{
			Token: cfg.WebUI.Token,
		})
		addr := fmt.Sprintf("%s:%d", cfg.WebUI.Host, cfg.WebUI.Port)
		go func() {
			slog.Info("WebUI listening", "addr", "http://"+addr)
			if err := srv.Run(ctx, addr); err != nil {
				slog.Error("api server", "err", err)
			}
		}()
	}

	cronLoader := engine.NewCronLoader(cfg.Workspace.CronsDir)
	scheduler := engine.NewScheduler(cfg, agent, rt, skills, mainInbox, cronLoader)

	slog.Info("bot started", "model", cfg.LLM.Model)
	if err := scheduler.Run(ctx); err != nil && err != context.Canceled {
		slog.Error("scheduler", "err", err)
		os.Exit(1)
	}
}

func buildRuntime(cfg *config.Config) runtime.Runtime {
	if cfg.Container.Enabled {
		rt, err := runtime.NewContainerRuntime(cfg.Container.Name, cfg.Container.Runtime, "/workspace", cfg.Tool.MaxOutputChars)
		if err != nil {
			slog.Error("container runtime unavailable", "err", err)
			os.Exit(1)
		}
		slog.Info("using container runtime", "container", cfg.Container.Name)
		return rt
	}
	slog.Info("using host runtime")
	return runtime.NewHostRuntime(cfg.Tool.MaxOutputChars)
}

func buildLLMClient(cfg *config.Config) llm.CompletionClient {
	return llm.NewOpenAIProvider(cfg.LLM.BaseURL, cfg.LLM.APIKey)
}
