package llm

import (
	"context"
	"os"

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

func (l *AgentLoop) Run(ctx context.Context, reg *tools.Registry, orch Orchestrator, prompt SystemPrompt, content any) error {
	l.conv.Messages = append(l.conv.Messages, Message{"role": "user", "content": content})
	return l.agent.Run(ctx, l.cfg, prompt.Build(ctx), l.conv, orch, reg)
}

func (l *AgentLoop) Compress(ctx context.Context, prompt SystemPrompt) error {
	return l.agent.Compress(ctx, l.cfg, prompt.Build(ctx), l.conv)
}

func (l *AgentLoop) DumpConversation(path string) error {
	data, err := util.ToJSON(l.conv.Messages)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}
