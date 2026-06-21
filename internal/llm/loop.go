package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"my-bot/internal/config"
	"my-bot/internal/tools"
	"my-bot/internal/util"
)

type AgentLoop struct {
	cfg   *config.Config
	agent *Agent
	conv  *Conversation
}

func NewAgentLoop(cfg *config.Config, agent *Agent) *AgentLoop {
	return &AgentLoop{
		cfg:   cfg,
		agent: agent,
		conv:  NewConversation(),
	}
}

func (l *AgentLoop) TotalTokens() int64 { return l.conv.TotalTokens }

func (l *AgentLoop) ResetConv() { l.conv = NewConversation() }

func (l *AgentLoop) Run(ctx context.Context, abortCh <-chan struct{}, reg *tools.Registry, orch Orchestrator, prompt SystemPrompt, content any) error {
	l.conv.Messages = append(l.conv.Messages, Message{"role": "user", "content": content})
	return l.agent.Run(ctx, abortCh, l.cfg, prompt.Build(ctx), l.conv, orch, reg)
}

func (l *AgentLoop) Compress(ctx context.Context, prompt SystemPrompt) error {
	return l.agent.Compress(ctx, l.cfg, prompt.Build(ctx), l.conv)
}

func (l *AgentLoop) DumpConversation(path string) error {
	data, err := util.ToJSON(l.conv.Messages)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func (l *AgentLoop) LoadConversation(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var messages []Message
	if err := json.Unmarshal(data, &messages); err != nil {
		return fmt.Errorf("unmarshal conversation: %w", err)
	}
	l.conv = &Conversation{Messages: messages}
	return nil
}
