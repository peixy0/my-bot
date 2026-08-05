package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"my-bot/internal/config"
	"my-bot/internal/llm"
	"my-bot/internal/tools"
	"my-bot/internal/util"

	"golang.org/x/time/rate"
)

// ErrAborted is returned when the agent loop is aborted via the abort channel.
var ErrAborted = errors.New("aborted")

type Agent struct {
	client   llm.CompletionClient
	provider *llm.OpenAIProvider
	limiter  *rate.Limiter
}

type preparedToolCall struct {
	call     llm.ToolCall
	tool     tools.PreparedTool
	parallel bool
	err      error
}

func NewAgent(client llm.CompletionClient, limiter *rate.Limiter) *Agent {
	provider, _ := client.(*llm.OpenAIProvider)
	return &Agent{client: client, provider: provider, limiter: limiter}
}

func (a *Agent) Models(ctx context.Context) ([]string, error) {
	if a.provider == nil {
		return nil, errors.New("model listing is not supported by the configured LLM provider")
	}
	return a.provider.Models(ctx)
}

func prepareToolCalls(r *tools.Registry, calls []llm.ToolCall) []preparedToolCall {
	prepared := make([]preparedToolCall, len(calls))
	for i, call := range calls {
		preparer, found := r.Get(call.Name)
		var tool tools.PreparedTool
		var err error
		if !found {
			err = fmt.Errorf("unknown tool %q", call.Name)
			tool = tools.PreparedTool{
				Description: fmt.Sprintf("Calling unknown tool: %q", call.Name),
				Execute: func(ctx context.Context) (tools.ToolResult, error) {
					return tools.ToolResult{}, err
				},
			}
		} else {
			tool, err = preparer(call.Args)
		}
		prepared[i] = preparedToolCall{call: call, tool: tool, parallel: r.IsParallel(call.Name), err: err}
	}
	return prepared
}

func toolDescriptions(calls []preparedToolCall) []string {
	descriptions := make([]string, len(calls))
	for i, call := range calls {
		descriptions[i] = call.tool.Description
	}
	return descriptions
}

func (a *Agent) Run(
	ctx context.Context,
	abortCh <-chan struct{},
	cfg *config.Config,
	systemPrompt string,
	conv *llm.Conversation,
	orch Orchestrator,
	reg *tools.Registry,
) error {
	abortCtx, abortCancel := context.WithCancel(ctx)
	defer abortCancel()

	if abortCh != nil {
		go func() {
			select {
			case <-abortCh:
				abortCancel()
			case <-abortCtx.Done():
			}
		}()
	}

	systemMessages := []llm.ChatMessage{
		{Role: "system", Content: systemPrompt},
	}

	for {
		if abortCtx.Err() == context.Canceled {
			return ErrAborted
		}

		allMessages := append(systemMessages, conv.Messages...)
		model := cfg.LLM.Model
		slog.Debug("llm request", "model", model, "messages", len(allMessages), "tools", len(reg.Schemas()))
		if a.limiter != nil {
			err := a.limiter.Wait(abortCtx)
			if err != nil {
				if abortCtx.Err() == context.Canceled {
					return ErrAborted
				}
				return err
			}
		}
		resp, err := a.client.Complete(abortCtx, llm.CompletionRequest{
			Model:          model,
			Messages:       allMessages,
			Tools:          reg.Schemas(),
			MaxTokens:      cfg.Context.MaxOutputTokens,
			Temperature:    cfg.LLM.Temperature,
			TopP:           cfg.LLM.TopP,
			TopK:           cfg.LLM.TopK,
			ExtraBody:      cfg.LLM.ExtraBody,
			OnContentBegin: orch.OnContentBegin,
			OnContentDelta: orch.OnContentDelta,
		})
		if err != nil {
			if abortCtx.Err() == context.Canceled {
				return ErrAborted
			}
			return err
		}

		conv.TotalTokens = resp.TotalTokens
		slog.Debug("llm response",
			"finish_reason", resp.FinishReason,
			"tool_calls", len(resp.ToolCalls),
			"total_tokens", resp.TotalTokens,
			"content", resp.Content,
		)

		if len(resp.ToolCalls) == 0 {
			if resp.Content == "" {
				slog.Warn("got empty llm response, retrying")
				continue
			}
			assistantMsg := llm.AssistantMessage(resp.Content, resp.ReasoningContent, resp.ToolCalls)
			conv.Messages = append(conv.Messages, assistantMsg)
			orch.OnContentFinal(ctx)
			orch.OnFinalResponse(ctx, resp.Content)
			return nil
		}

		prepared := prepareToolCalls(reg, resp.ToolCalls)
		var toolDesc []string
		if cfg.Tool.EnableDescriptiveOutput {
			toolDesc = toolDescriptions(prepared)
		}
		orch.BeforeToolUse(abortCtx, resp.Content, toolDesc)
		outcomes, err := orch.DispatchTools(abortCtx, prepared)
		if err != nil {
			return err
		}

		var toolCalls []llm.ToolCall
		var toolMsgs []llm.ChatMessage
		var followups []llm.ChatMessage
		for i, oc := range outcomes {
			if oc.Err != nil {
				if cfg.LLM.SkipOnToolDispatchError {
					slog.Warn("dispatch error, skipping", "tool", resp.ToolCalls[i].Name, "id", resp.ToolCalls[i].ID, "err", oc.Err)
				} else {
					toolCalls = append(toolCalls, resp.ToolCalls[i])
					toolMsgs = append(toolMsgs, llm.ToolResultMessage(resp.ToolCalls[i].ID, fmt.Sprintf("tool dispatch error: %v", oc.Err)))
				}
				continue
			}
			toolCalls = append(toolCalls, resp.ToolCalls[i])
			toolMsgs = append(toolMsgs, oc.ToolMsg)
			if oc.Followup != nil {
				followups = append(followups, *oc.Followup)
			}
		}
		if len(toolMsgs) == 0 {
			slog.Warn("got invalid tool call response, retrying")
			continue
		}

		assistantMsg := llm.AssistantMessage(resp.Content, resp.ReasoningContent, toolCalls)
		conv.Messages = append(conv.Messages, assistantMsg)
		orch.OnContentFinal(ctx)

		if threshold := int(float64(cfg.LLM.ContextWindow) * cfg.Context.CompressionThreshold); threshold > 0 && int(conv.TotalTokens) >= threshold {
			slog.Debug("context compression triggered", "tokens", conv.TotalTokens, "context_window", cfg.LLM.ContextWindow)
			if err := a.Compress(abortCtx, cfg, systemPrompt, conv); err != nil {
				if abortCtx.Err() == context.Canceled {
					return ErrAborted
				}
				return fmt.Errorf("compress: %w", err)
			}
			slog.Debug("context compression done", "tokens", conv.TotalTokens)
		}

		conv.Messages = append(conv.Messages, toolMsgs...)
		conv.Messages = append(conv.Messages, followups...)

		if interrupt := orch.MaybeInterrupt(ctx); interrupt != nil {
			conv.Messages = append(conv.Messages, *interrupt)
		}
	}
}

func (a *Agent) Compress(ctx context.Context, cfg *config.Config, systemPrompt string, conv *llm.Conversation) error {
	if len(conv.Messages) < 2 {
		return nil
	}

	toEvict := conv.Messages[:len(conv.Messages)-1]
	retained := conv.Messages[len(conv.Messages)-1]

	compressMessages := buildCompressionMessages(systemPrompt, toEvict)
	resp, err := a.client.Complete(ctx, llm.CompletionRequest{
		Model:       cfg.LLM.Model,
		Messages:    compressMessages,
		MaxTokens:   cfg.Context.MaxOutputTokens,
		Temperature: compressionTemperature,
		TopP:        cfg.LLM.TopP,
		TopK:        cfg.LLM.TopK,
		ExtraBody:   cfg.LLM.ExtraBody,
	})
	if err != nil {
		return err
	}

	anchor := strings.TrimSpace(resp.Content)
	if anchor == "" {
		return fmt.Errorf("got empty anchor")
	}
	conv.Messages = []llm.ChatMessage{
		{Role: "user", Content: "[CONTEXT ANCHOR]\n" + anchor},
		retained,
	}
	conv.TotalTokens = resp.TotalTokens
	return nil
}

const compressionTemperature = 0.2

const compressionInstruction = "You are compressing context for an autonomous AI agent.\n" +
	"Summarize and merge ALL prior messages in this request into one persistent anchor state.\n\n" +
	"You may receive an existing [CONTEXT ANCHOR] from an earlier compression cycle. Treat it as already-compressed prior history and merge newer messages into it.\n\n" +
	"Rules:\n" +
	"- Preserve ALL exact file paths, command outputs, error messages, identifiers, and concrete values; the agent resumes work from this state.\n" +
	"- Escalate completed tasks from Active to Completed; remove fully resolved issues.\n" +
	"- Be dense. Omit pleasantries, exploratory dead-ends, and superseded decisions.\n" +
	"- Write in third-person past tense using the language of the original conversation.\n" +
	"- The agent will have NO other context beyond this anchor and the retained latest message, so every section must be self-contained and actionable.\n\n" +
	"Output ONLY the updated anchor state using these Markdown sections, omitting any section that would be empty:\n\n" +
	"## Intent\n" +
	"The original goal and any refined sub-goals still in scope.\n\n" +
	"## Quotes\n" +
	"Verbatim excerpts from the conversation that are critical to preserve, such as exact user words, numbers, and information.\n\n" +
	"## Immediate Next Step\n" +
	"The single most important concrete action to take when resuming, stated as an unambiguous, actionable step.\n\n" +
	"## Active Tasks\n" +
	"Ongoing work and the immediate next steps.\n\n" +
	"## Completed Tasks\n" +
	"Finished actions and their concrete outcomes.\n\n" +
	"## Decisions\n" +
	"Design choices, accepted approaches, and their rationale.\n\n" +
	"## Needed Skills\n" +
	"Skills in-use for current tasks and should be loaded explicitly.\n\n" +
	"## Key Files\n" +
	"Files created, modified, or read, with their path and purpose.\n\n" +
	"## Established Facts\n" +
	"Specific paths, versions, identifiers, env state, and discovered values.\n\n" +
	"## Pending Issues\n" +
	"Unresolved errors, blockers, or open questions."

func buildCompressionMessages(systemPrompt string, messages []llm.ChatMessage) []llm.ChatMessage {
	out := make([]llm.ChatMessage, 0, len(messages)+2)
	out = append(out, llm.ChatMessage{Role: "system", Content: systemPrompt})
	out = append(out, messages...)
	out = append(out, llm.UserMessage(compressionInstruction))
	return out
}

// AgentLoop manages a per-session conversation loop.
type AgentLoop struct {
	cfg   *config.Config
	agent *Agent
	conv  *llm.Conversation
}

func NewAgentLoop(cfg *config.Config, agent *Agent) *AgentLoop {
	return &AgentLoop{
		cfg:   cfg,
		agent: agent,
		conv:  llm.NewConversation(),
	}
}

func (l *AgentLoop) TotalTokens() int64 { return l.conv.TotalTokens }

func (l *AgentLoop) ResetConv() { l.conv = llm.NewConversation() }

func (l *AgentLoop) Run(ctx context.Context, abortCh <-chan struct{}, reg *tools.Registry, orch Orchestrator, prompt llm.SystemPrompt, content any) error {
	l.conv.Messages = append(l.conv.Messages, llm.ChatMessage{Role: "user", Content: content})
	return l.agent.Run(ctx, abortCh, l.cfg, prompt.Build(ctx), l.conv, orch, reg)
}

func (l *AgentLoop) Compress(ctx context.Context, prompt llm.SystemPrompt) error {
	return l.agent.Compress(ctx, l.cfg, prompt.Build(ctx), l.conv)
}

func (l *AgentLoop) DumpConversation(path string) error {
	data, err := util.ToJSON(l.conv)
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
	var conv llm.Conversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return fmt.Errorf("unmarshal conversation: %w", err)
	}
	l.conv = &conv
	return nil
}
