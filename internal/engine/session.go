package engine

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/llm"
	"my-bot/internal/runtime"
	"my-bot/internal/tasks"
	"my-bot/internal/tools"
)

type sessionTools struct {
	rt          runtime.Runtime
	skills      *tools.SkillLoader
	cfg         *config.Config
	agent       *llm.Agent
	taskManager *tasks.Manager
	cmdTools    *tools.CommandToolset
}

func newSessionTools(rt runtime.Runtime, skills *tools.SkillLoader, cfg *config.Config, agent *llm.Agent) *sessionTools {
	taskManager := tasks.NewManager(cfg.Tool.MaxOutputChars)
	return &sessionTools{
		rt:          rt,
		skills:      skills,
		cfg:         cfg,
		agent:       agent,
		taskManager: taskManager,
		cmdTools:    tools.NewCommandToolset(rt, taskManager),
	}
}

func (t *sessionTools) Registry(sender events.Outbound) *tools.Registry {
	return llm.NewSessionRegistry(t.rt, t.skills, t.cfg, t.agent, t.taskManager, t.cmdTools, sender)
}

func (t *sessionTools) Shutdown(ctx context.Context) error {
	return t.taskManager.Shutdown(ctx)
}

type chatSession struct {
	chatID string
	worker *ConversationWorker
	tools  *sessionTools
	cron   *CronWorker
	cancel context.CancelFunc
}

func newChatSession(
	parent context.Context,
	chatID string,
	cfg *config.Config,
	agent *llm.Agent,
	rt runtime.Runtime,
	skills *tools.SkillLoader,
	cronLoader *CronLoader,
) *chatSession {
	tools := newSessionTools(rt, skills, cfg, agent)
	worker := newConversationWorker(chatID, cfg, agent, rt, skills, tools)
	workerCtx, cancel := context.WithCancel(parent)
	session := &chatSession{
		chatID: chatID,
		worker: worker,
		tools:  tools,
		cancel: cancel,
	}
	if cronLoader != nil {
		session.cron = NewCronWorker(chatID, worker.Events, cronLoader)
	}
	go session.run(workerCtx)
	return session
}

func (s *chatSession) run(ctx context.Context) {
	defer s.shutdown()
	if err := s.worker.Run(ctx); err != nil && err != context.Canceled {
		slog.Error("worker exited", "chat", s.chatID, "err", err)
	}
}

func (s *chatSession) close() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *chatSession) publishEvent(ev events.WorkerEvent) bool {
	if s.worker.Events.TryPublish(ev) {
		return true
	}
	slog.Error("worker event dropped: channel full", "chat", s.chatID, "event", fmt.Sprintf("%T", ev))
	return false
}

func (s *chatSession) publishMessage(ev events.MessageEvent) bool {
	if s.worker.MessageInbox.TryPublish(ev) {
		return true
	}
	slog.Error("message event dropped: channel full", "chat", s.chatID, "event", fmt.Sprintf("%T", ev))
	return false
}

func (s *chatSession) tryAbort(ctx context.Context, sender events.Outbound) bool {
	select {
	case s.worker.abortCh <- struct{}{}:
		sender.Send(ctx, "abort requested")
		return true
	default:
		return false
	}
}

func (s *chatSession) cronWorker() *CronWorker {
	return s.cron
}

func (s *chatSession) shutdown() {
	if s.cron != nil {
		s.cron.Stop()
	}
	if s.tools == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.tools.Shutdown(ctx); err != nil {
		slog.Error("shutdown", "chat", s.chatID, "err", err)
	}
}
