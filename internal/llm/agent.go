package llm

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"my-bot/internal/config"
	"my-bot/internal/tools"
)

type Agent struct {
	client CompletionClient
}

func NewAgent(client CompletionClient) *Agent {
	return &Agent{client: client}
}

func (a *Agent) Fork() *Agent {
	return NewAgent(a.client)
}

func (a *Agent) Run(
	ctx context.Context,
	cfg *config.Config,
	systemPrompt string,
	conv *Conversation,
	orch Orchestrator,
	reg *tools.Registry,
) error {
	systemMessages := []Message{
		{"role": "system", "content": systemPrompt},
	}

	for {
		allMessages := append(systemMessages, conv.Messages...)
		model := cfg.OpenAIModel
		slog.Debug("llm request", "model", model, "messages", len(allMessages), "tools", len(reg.Schemas()))
		resp, err := a.client.Complete(ctx, CompletionRequest{
			Model:          model,
			Messages:       allMessages,
			Tools:          reg.Schemas(),
			MaxTokens:      cfg.ContextMaxTokens,
			Temperature:    cfg.Temperature,
			TopP:           cfg.TopP,
			TopK:           cfg.TopK,
			OnContentDelta: orch.OnContentDelta,
		})
		if err != nil {
			return err
		}

		conv.TotalTokens = resp.TotalTokens
		slog.Debug("llm response",
			"finish_reason", resp.FinishReason,
			"tool_calls", len(resp.ToolCalls),
			"total_tokens", resp.TotalTokens,
			"content", resp.Content,
		)

		assistantMsg := assistantMessage(resp.Content, resp.ReasoningContent, resp.ToolCalls)
		conv.Messages = append(conv.Messages, assistantMsg)
		orch.OnContentFinal(ctx, resp.Content)

		if len(resp.ToolCalls) == 0 {
			orch.OnFinalResponse(ctx, resp.Content)
			return nil
		}

		if cfg.ContextAutoCompressionEnabled && int(conv.TotalTokens) >= int(float64(cfg.ContextMaxTokens)*cfg.ContextCompressionThreshold) {
			slog.Debug("context compression triggered", "tokens", conv.TotalTokens, "max", cfg.ContextMaxTokens)
			if err := a.Compress(ctx, cfg, systemPrompt, conv); err != nil {
				return fmt.Errorf("compress: %w", err)
			}
			slog.Debug("context compression done", "tokens", conv.TotalTokens)
		}

		orch.BeforeToolUse(ctx, resp.Content)
		toolMsgs, err := orch.DispatchTools(ctx, resp.ToolCalls)
		if err != nil {
			return err
		}
		conv.Messages = append(conv.Messages, toolMsgs...)
	}
}

func (a *Agent) Compress(ctx context.Context, cfg *config.Config, systemPrompt string, conv *Conversation) error {
	if len(conv.Messages) < 2 {
		return nil
	}

	toEvict := conv.Messages[:len(conv.Messages)-1]
	retained := conv.Messages[len(conv.Messages)-1]

	compressMessages := buildCompressionMessages(systemPrompt, toEvict)
	resp, err := a.client.Complete(ctx, CompletionRequest{
		Model:       cfg.OpenAIModel,
		Messages:    compressMessages,
		MaxTokens:   cfg.ContextMaxTokens,
		Temperature: compressionTemperature,
		TopP:        cfg.TopP,
		TopK:        cfg.TopK,
	})
	if err != nil {
		return err
	}

	anchor := strings.TrimSpace(resp.Content)
	conv.Messages = []Message{
		{"role": "user", "content": "[CONTEXT ANCHOR]\n" + anchor},
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
	"## Key Files\n" +
	"Files created, modified, or read, with their path and purpose.\n\n" +
	"## Established Facts\n" +
	"Specific paths, versions, identifiers, env state, and discovered values.\n\n" +
	"## Pending Issues\n" +
	"Unresolved errors, blockers, or open questions."

func buildCompressionMessages(systemPrompt string, messages []Message) []Message {
	out := make([]Message, 0, len(messages)+2)
	out = append(out, Message{"role": "system", "content": systemPrompt})
	out = append(out, messages...)
	out = append(out, userMessage(compressionInstruction))
	return out
}
