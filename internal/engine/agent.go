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
	"my-bot/internal/events"
	"my-bot/internal/llm"
	"my-bot/internal/tools"
	"my-bot/internal/util"

	"golang.org/x/time/rate"
)

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
	descriptions := make([]string, 0, len(calls))
	for _, call := range calls {
		if call.tool.Description != "" {
			descriptions = append(descriptions, call.tool.Description)
		}
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
			"completion_tokens", resp.CompletionTokens,
			"generation_time", resp.GenerationTime,
			"content", resp.Content,
		)

		if len(resp.ToolCalls) == 0 {
			if resp.Content == "" {
				slog.Warn("got empty llm response, retrying")
				continue
			}
			assistantMsg := llm.AssistantMessage(resp.Content, resp.ReasoningContent, resp.ToolCalls)
			conv.Messages = append(conv.Messages, assistantMsg)
			metadata := responseMetadata(model, cfg.LLM.ContextWindow, resp)
			orch.OnContentFinal(ctx, metadata)
			orch.OnFinalResponse(ctx, resp.Content, metadata)
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
		orch.OnContentFinal(ctx, nil)

		if threshold := int(float64(cfg.LLM.ContextWindow) * cfg.Context.CompressionThreshold); threshold > 0 && int(conv.TotalTokens) >= threshold {
			slog.Debug("in loop context compression start", "tokens", conv.TotalTokens, "context_window", cfg.LLM.ContextWindow)
			if err := a.Compress(abortCtx, cfg, conv); err != nil {
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

func responseMetadata(model string, contextWindow int64, resp llm.CompletionResponse) *events.ResponseMetadata {
	return &events.ResponseMetadata{
		Model:            model,
		PromptTokens:     resp.PromptTokens,
		CompletionTokens: resp.CompletionTokens,
		TotalTokens:      resp.TotalTokens,
		ContextWindow:    contextWindow,
		GenerationTime:   resp.GenerationTime,
	}
}

func (a *Agent) Compress(ctx context.Context, cfg *config.Config, conv *llm.Conversation) error {
	if len(conv.Messages) < 2 {
		return nil
	}

	retainedStart := findLastGroupStart(conv.Messages)
	flatText := flattenMessages(conv.Messages[:retainedStart], cfg.Context.CompressionToolResultTruncate)
	flatText = "[CONTEXT COMPRESSION TASK]\nCompress the following conversation into a state anchor.\nDo not continue the conversation.\n\n[CONVERSATION HISTORY BEGIN]\n\n" + flatText + "\n\n[CONVERSATION HISTORY END]"

	compressionCfg := cfg.CompressionLLMConfig()
	resp, err := a.client.Complete(ctx, llm.CompletionRequest{
		Model:       compressionCfg.Model,
		Messages:    []llm.ChatMessage{{Role: "system", Content: llm.CompressionInstruction}, llm.UserMessage(flatText)},
		Tools:       []map[string]any{},
		MaxTokens:   cfg.Context.MaxOutputTokens,
		Temperature: compressionCfg.Temperature,
		TopP:        compressionCfg.TopP,
		TopK:        compressionCfg.TopK,
		ExtraBody:   compressionCfg.ExtraBody,
	})
	if err != nil {
		return err
	}

	anchor := strings.TrimSpace(resp.Content)
	if anchor == "" {
		return fmt.Errorf("got empty anchor")
	}
	conv.Messages = append([]llm.ChatMessage{
		{Role: "user", Content: "[CONTEXT ANCHOR]\n" + anchor},
	}, conv.Messages[retainedStart:]...)
	conv.TotalTokens = resp.TotalTokens
	return nil
}

func findLastGroupStart(messages []llm.ChatMessage) int {
	if len(messages) == 0 {
		return 0
	}
	i := len(messages) - 1
	for i > 0 && messages[i].Role == "tool" {
		i--
	}
	return i
}

func flattenMessages(messages []llm.ChatMessage, truncateLimit int) string {
	var b strings.Builder
	toolResults := map[string]string{}
	for _, m := range messages {
		if m.Role == "tool" {
			result := contentToString(m.Content)
			if truncateLimit > 0 && len(result) > truncateLimit {
				result = result[:truncateLimit] + "..."
			}
			toolResults[m.ToolCallID] = result
		}
	}
	for i, m := range messages {
		if m.Role == "tool" {
			continue
		}
		if i > 0 {
			b.WriteString("\n\n")
		}
		switch m.Role {
		case "user":
			b.WriteString("## User\n")
			b.WriteString(contentToString(m.Content))
		case "assistant":
			b.WriteString("## Assistant\n")
			b.WriteString(contentToString(m.Content))
			for _, tc := range m.ToolCalls {
				b.WriteString("\n## Tool ")
				b.WriteString(tc.Function.Name)
				b.WriteString("(")
				b.WriteString(tc.Function.Arguments)
				b.WriteString(")\n")
				b.WriteString(toolResults[tc.ID])
			}
		default:
		}
	}
	return b.String()
}

func contentToString(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []map[string]any:
		return contentPartsToString(v)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func contentPartsToString[T any](parts []T) string {
	var out []string
	for _, part := range parts {
		p, ok := any(part).(map[string]any)
		if !ok {
			out = append(out, fmt.Sprintf("%v", part))
			continue
		}
		ptype, _ := p["type"].(string)
		if ptype == "text" {
			if text, ok := p["text"].(string); ok {
				out = append(out, text)
			} else {
				out = append(out, ptype)
			}
		} else {
			out = append(out, "["+ptype+"]")
		}
	}
	return strings.Join(out, "\n")
}

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

func (l *AgentLoop) Compress(ctx context.Context) error {
	return l.agent.Compress(ctx, l.cfg, l.conv)
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

func (l *AgentLoop) HasConversation() bool {
	return len(l.conv.Messages) > 0
}

func (l *AgentLoop) TrimLastMessage() {
	i := findLastGroupStart(l.conv.Messages)
	l.conv.Messages = l.conv.Messages[:i]
}
