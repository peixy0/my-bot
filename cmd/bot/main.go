package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"my-bot/internal/api"
	"my-bot/internal/browser"
	"my-bot/internal/config"
	"my-bot/internal/engine"
	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/llm"
	"my-bot/internal/messaging/feishu"
	"my-bot/internal/messaging/wechat"
	"my-bot/internal/runtime"
	"my-bot/internal/tools"

	"golang.org/x/time/rate"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	launchDir, err := os.Getwd()
	if err != nil {
		slog.Error("get launch directory", "err", err)
		os.Exit(1)
	}

	configPath := "config.yaml"
	flag.StringVar(&configPath, "c", configPath, "config file path")
	flag.Parse()
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		slog.Error("config path", "err", err)
		os.Exit(1)
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
	browserBroker := buildBrowserBroker(cfg, rt)
	go func() {
		if err := browserBroker.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("browser broker", "err", err)
		}
	}()

	llmClient := buildLLMClient(cfg)
	limiter := buildLimiter(cfg)
	agent := engine.NewAgent(llmClient, limiter)

	mainInbox := inbox.NewMemory[events.AgentEvent](256)

	if cfg.Feishu != nil {
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

	if cfg.WeChat != nil {
		wcCfg := wechat.Config{
			BotToken: cfg.WeChat.BotToken,
			BaseURL:  cfg.WeChat.BaseURL,
		}
		wcInbound := wechat.NewInbound(wcCfg, mainInbox, rt)
		go func() {
			if err := wcInbound.Run(ctx); err != nil && err != context.Canceled {
				slog.Error("wechat inbound", "err", err)
			}
		}()
	}

	if cfg.WebUI.Enabled {
		srv := api.NewServer(mainInbox, rt, api.ServerOptions{
			Token:         cfg.WebUI.Token,
			IndexHTMLPath: cfg.WebUI.IndexHTMLPath,
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
	scheduler := engine.NewScheduler(engine.SessionEnv{
		Cfg:           cfg,
		Rt:            rt,
		Agent:         agent,
		Skills:        skills,
		BrowserBroker: browserBroker,
	}, mainInbox, cronLoader)

	slog.Info("bot started")
	if err := scheduler.Run(ctx); errors.Is(err, engine.ErrReboot) {
		if execErr := os.Chdir(launchDir); execErr != nil {
			slog.Error("restore launch directory", "err", execErr)
			os.Exit(1)
		}
		executable, execErr := os.Executable()
		if execErr != nil {
			slog.Error("find executable", "err", execErr)
			os.Exit(1)
		}
		if execErr := syscall.Exec(executable, restartArgs(executable, configPath), os.Environ()); execErr != nil {
			slog.Error("reboot", "err", execErr)
			os.Exit(1)
		}
		slog.Info("bot rebooting")
	} else if err != nil && err != context.Canceled {
		slog.Error("scheduler", "err", err)
		os.Exit(1)
	}
}

func restartArgs(executable, configPath string) []string {
	return []string{executable, "-c", configPath}
}

func buildRuntime(cfg *config.Config) runtime.Runtime {
	if cfg.Container != nil {
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

func buildBrowserBroker(cfg *config.Config, rt runtime.Runtime) browser.Broker {
	browserCfg := cfg.Browser
	if browserCfg == nil || !browserCfg.Enabled {
		return browser.NewNoopBroker()
	}
	broker := browser.NewExtensionBroker(browser.Config{
		ListenAddr: browserCfg.ListenAddr,
		Path:       browserCfg.Path,
	}, rt, cfg.Tool.MaxOutputChars)
	return broker
}

func buildLLMClient(cfg *config.Config) llm.CompletionClient {
	return llm.NewOpenAIProvider(cfg.LLM.BaseURL, cfg.LLM.APIKey)
}

func buildLimiter(cfg *config.Config) *rate.Limiter {
	if cfg.Limiter == nil {
		return nil
	}
	return rate.NewLimiter(rate.Every(time.Minute/time.Duration(cfg.Limiter.RPM)), cfg.Limiter.Burst)
}
