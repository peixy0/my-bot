package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"my-bot/internal/browser"
	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/runtime"
	"my-bot/internal/tasks"
	"my-bot/internal/tools"
)

type SessionEnv struct {
	Cfg           *config.Config
	Rt            runtime.Runtime
	Agent         *Agent
	Skills        *tools.SkillLoader
	BrowserBroker browser.Broker
}

type sessionTools struct {
	env           SessionEnv
	taskManager   *tasks.Manager
	cmdTools      *tools.CommandToolset
	browserClient browser.Client
}

func newSessionTools(env SessionEnv) *sessionTools {
	taskManager := tasks.NewManager(env.Rt, env.Cfg.Tool.MaxOutputChars)
	browserClient := env.BrowserBroker.NewClient()
	return &sessionTools{
		env:           env,
		taskManager:   taskManager,
		cmdTools:      tools.NewCommandToolset(env.Rt, taskManager),
		browserClient: browserClient,
	}
}

func (t *sessionTools) BuildRegistry(sender events.Outbound) *tools.Registry {
	return NewSessionRegistry(t.env, t.taskManager, t.cmdTools, t.browserClient, sender)
}

func (t *sessionTools) Shutdown(ctx context.Context) error {
	return errors.Join(
		t.browserClient.Close(ctx),
		t.taskManager.Shutdown(ctx),
	)
}

type chatSession struct {
	chatID string
	worker *ConversationWorker
	tools  *sessionTools
	cron   *CronWorker
	cancel context.CancelFunc
	done   chan struct{}
}

func newChatSession(
	parent context.Context,
	chatID string,
	env SessionEnv,
	cronLoader *CronLoader,
) *chatSession {
	tools := newSessionTools(env)
	worker := newConversationWorker(chatID, env, tools)
	workerCtx, cancel := context.WithCancel(parent)
	session := &chatSession{
		chatID: chatID,
		worker: worker,
		tools:  tools,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	if cronLoader != nil {
		session.cron = NewCronWorker(chatID, worker.Events, cronLoader)
	}
	go session.run(workerCtx)
	return session
}

func (s *chatSession) run(ctx context.Context) {
	defer close(s.done)
	defer s.shutdown()
	if err := s.worker.Run(ctx); err != nil && err != context.Canceled {
		slog.Error("worker exited", "chat_id", s.chatID, "err", err)
	}
}

func (s *chatSession) close() {
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *chatSession) wait(ctx context.Context) error {
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *chatSession) publishEvent(ev events.WorkerEvent) bool {
	target := s.worker.Events
	if isControlEvent(ev) {
		target = s.worker.Control
	}
	if target != nil && target.TryPublish(ev) {
		return true
	}
	slog.Error("worker event dropped: channel full", "chat_id", s.chatID, "event", fmt.Sprintf("%T", ev))
	return false
}

func isControlEvent(ev events.WorkerEvent) bool {
	switch ev.(type) {
	case events.NewSessionEvent,
		events.ConfigQueryEvent,
		events.ConfigChangeEvent,
		events.DumpCommand,
		events.ResumeCommand,
		events.RebootCommand:
		return true
	default:
		return false
	}
}

func (s *chatSession) snapshot(ctx context.Context, id string) error {
	result := make(chan error, 1)
	if !s.publishEvent(events.DumpCommand{ID: id, Result: result}) {
		return errors.New("snapshot request rejected")
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *chatSession) stopAndSnapshot(ctx context.Context, id string) error {
	result := make(chan error, 1)
	if !s.publishEvent(events.RebootCommand{ID: id, Result: result}) {
		return errors.New("reboot request rejected")
	}
	select {
	case err := <-result:
		return err
	case s.worker.abortCh <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *chatSession) restore(ctx context.Context, id string) error {
	result := make(chan error, 1)
	if !s.publishEvent(events.ResumeCommand{ID: id, Result: result}) {
		return errors.New("restore request rejected")
	}
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *chatSession) publishMessage(ev events.MessageEvent) bool {
	if s.worker.MessageInbox.TryPublish(ev) {
		return true
	}
	slog.Error("message event dropped: channel full", "chat_id", s.chatID, "event", fmt.Sprintf("%T", ev))
	return false
}

func (s *chatSession) tryAbort() bool {
	select {
	case s.worker.abortCh <- struct{}{}:
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
		slog.Error("shutdown session resources", "chat_id", s.chatID, "err", err)
	}
}
