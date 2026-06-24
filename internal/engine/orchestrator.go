package engine

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/llm"
	"my-bot/internal/tasks"
	"my-bot/internal/tools"

	"golang.org/x/sync/errgroup"
)

type HumanInputOrchestrator struct {
	registry      *tools.Registry
	sender        events.Outbound
	inLoopInbox   inbox.Inbox[events.MessageEvent]
	visionSupport bool
}

func NewHumanInputOrchestrator(registry *tools.Registry, sender events.Outbound, inLoopInbox inbox.Inbox[events.MessageEvent]) *HumanInputOrchestrator {
	return &HumanInputOrchestrator{registry: registry, sender: sender, inLoopInbox: inLoopInbox}
}

func (o *HumanInputOrchestrator) WithVision(visionSupport bool) *HumanInputOrchestrator {
	o.visionSupport = visionSupport
	return o
}

func (o *HumanInputOrchestrator) OnContentDelta(ctx context.Context, delta string) {
	if o.sender == nil || delta == "" {
		return
	}
	o.sender.SendDelta(ctx, delta)
}

func (o *HumanInputOrchestrator) OnContentFinal(ctx context.Context, _ string) {
	if o.sender == nil {
		return
	}
	o.sender.SendFinal(ctx)
}

func (o *HumanInputOrchestrator) OnFinalResponse(_ context.Context, _ string) {}

func (o *HumanInputOrchestrator) BeforeToolUse(_ context.Context, _ string) {}

func (o *HumanInputOrchestrator) DispatchTools(ctx context.Context, calls []llm.ToolCall) ([]llm.ChatMessage, error) {
	toolMsgs, err := runDispatch(ctx, o.registry, calls)
	if err != nil {
		return nil, err
	}
	if inject := o.drainInLoopInput(); inject != nil {
		slog.Debug("in-loop user input injected after tool dispatch", "tools", len(calls))
		toolMsgs = append(toolMsgs, *inject)
	}
	return toolMsgs, nil
}

func (o *HumanInputOrchestrator) drainInLoopInput() *llm.ChatMessage {
	var items []events.MessageEvent
	for {
		msg, ok := o.inLoopInbox.TryReceive()
		if !ok {
			break
		}
		items = append(items, msg)
	}
	if len(items) == 0 {
		return nil
	}
	var sb strings.Builder
	sb.WriteString("[USER MESSAGE INTERRUPTING YOUR CURRENT WORK]\n")
	sb.WriteString("The user sent additional message while you were working. Read carefully and adjust your plan immediately if it changes what you should do next:\n\n")
	var imageParts []map[string]any
	for _, item := range items {
		switch ev := item.(type) {
		case events.TextInputEvent:
			sb.WriteString(ev.Message)
			sb.WriteString("\n\n")
		case events.ImageInputEvent:
			if !o.visionSupport {
				continue
			}
			if strings.TrimSpace(ev.Message) != "" {
				sb.WriteString(ev.Message)
				sb.WriteString("\n\n")
			}
			imageParts = appendVisionImagePart(imageParts, ev)
		}
	}
	text := strings.TrimSpace(sb.String())
	if len(imageParts) == 0 {
		msg := llm.UserMessage(text)
		return &msg
	}
	msg := llm.UserBlocksMessage(text, imageParts)
	return &msg
}

type BackgroundOrchestrator struct {
	registry *tools.Registry
	sender   events.Outbound
}

func NewBackgroundOrchestrator(registry *tools.Registry, sender events.Outbound) *BackgroundOrchestrator {
	return &BackgroundOrchestrator{registry: registry, sender: sender}
}

func (o *BackgroundOrchestrator) OnContentDelta(_ context.Context, _ string) {}

func (o *BackgroundOrchestrator) OnContentFinal(_ context.Context, _ string) {}

func (o *BackgroundOrchestrator) OnFinalResponse(ctx context.Context, content string) {
	const noReport = "NO_REPORT"
	if o.sender == nil || content == "" || strings.HasSuffix(strings.TrimSpace(content), noReport) {
		return
	}
	o.sender.Send(ctx, content)
}

func (o *BackgroundOrchestrator) BeforeToolUse(_ context.Context, _ string) {}

func (o *BackgroundOrchestrator) DispatchTools(ctx context.Context, calls []llm.ToolCall) ([]llm.ChatMessage, error) {
	return runDispatch(ctx, o.registry, calls)
}

type SubagentOrchestrator struct {
	registry *tools.Registry
	emit     *tasks.Emitter
	input    <-chan string
}

func NewSubagentOrchestrator(registry *tools.Registry, emit *tasks.Emitter, input <-chan string) *SubagentOrchestrator {
	return &SubagentOrchestrator{registry: registry, emit: emit, input: input}
}

func (o *SubagentOrchestrator) OnContentDelta(_ context.Context, delta string) {
	if delta != "" && o.emit != nil {
		o.emit.Output(delta)
	}
}

func (o *SubagentOrchestrator) OnContentFinal(_ context.Context, _ string) {}

func (o *SubagentOrchestrator) OnFinalResponse(_ context.Context, content string) {}

func (o *SubagentOrchestrator) BeforeToolUse(_ context.Context, _ string) {}

func (o *SubagentOrchestrator) DispatchTools(ctx context.Context, calls []llm.ToolCall) ([]llm.ChatMessage, error) {
	toolMsgs, err := runDispatch(ctx, o.registry, calls)
	if err != nil {
		return nil, err
	}
	if inject := o.drainInput(); inject != nil {
		toolMsgs = append(toolMsgs, *inject)
	}
	return toolMsgs, nil
}

func (o *SubagentOrchestrator) drainInput() *llm.ChatMessage {
	if o.input == nil {
		return nil
	}
	var items []string
	for {
		select {
		case text := <-o.input:
			if strings.TrimSpace(text) != "" {
				items = append(items, text)
			}
		default:
			if len(items) == 0 {
				return nil
			}
			msg := llm.UserMessage(strings.Join(items, "\n\n"))
			return &msg
		}
	}
}

func appendVisionImagePart(parts []map[string]any, ev events.ImageInputEvent) []map[string]any {
	return append(parts, map[string]any{
		"type": "image_url",
		"image_url": map[string]any{
			"url":    fmt.Sprintf("data:%s;base64,%s", ev.MIMEType, base64.StdEncoding.EncodeToString(ev.ImageData)),
			"detail": "auto",
		},
	})
}

type dispatchResult struct {
	toolMsg  llm.ChatMessage
	followup *llm.ChatMessage
}

func execOne(ctx context.Context, r *tools.Registry, tc llm.ToolCall) dispatchResult {
	handler, ok := r.Handler(tc.Name)
	if !ok {
		return dispatchResult{toolMsg: llm.ToolResultMessage(tc.ID, fmt.Sprintf("unknown tool %q", tc.Name))}
	}
	slog.Debug("tool call start", "tool", tc.Name, "id", tc.ID, "args", string(tc.Args))
	result, err := handler(ctx, tc.Args)
	if err != nil {
		slog.Debug("tool call error", "tool", tc.Name, "id", tc.ID, "err", err)
		return dispatchResult{toolMsg: llm.ToolResultMessage(tc.ID, fmt.Sprintf("error: %v", err))}
	}
	slog.Debug("tool call done", "tool", tc.Name, "id", tc.ID, "result", result.Text)
	if result.Blocks != nil {
		toolText := result.Text
		if strings.TrimSpace(toolText) == "" {
			toolText = fmt.Sprintf("%s returned non-text content. A follow-up user message contains the multimodal payload.", tc.Name)
		}
		followup := llm.UserBlocksMessage(
			fmt.Sprintf("[TOOL OUTPUT FROM %s]\nInspect the attached content and continue with the task.", tc.ID),
			result.Blocks,
		)
		return dispatchResult{
			toolMsg:  llm.ToolResultMessage(tc.ID, toolText),
			followup: &followup,
		}
	}
	return dispatchResult{toolMsg: llm.ToolResultMessage(tc.ID, result.Text)}
}

func runDispatch(ctx context.Context, r *tools.Registry, calls []llm.ToolCall) ([]llm.ChatMessage, error) {
	results := make([]dispatchResult, len(calls))

	if len(calls) == 1 {
		res := execOne(ctx, r, calls[0])
		msgs := []llm.ChatMessage{res.toolMsg}
		if res.followup != nil {
			msgs = append(msgs, *res.followup)
		}
		return msgs, nil
	}

	var seqMu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for i, tc := range calls {
		i, tc := i, tc
		g.Go(func() error {
			if r.IsParallel(tc.Name) {
				results[i] = execOne(gctx, r, tc)
			} else {
				seqMu.Lock()
				results[i] = execOne(gctx, r, tc)
				seqMu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	msgs := make([]llm.ChatMessage, 0, len(calls)*2)
	for _, res := range results {
		msgs = append(msgs, res.toolMsg)
	}
	for _, res := range results {
		if res.followup != nil {
			msgs = append(msgs, *res.followup)
		}
	}
	return msgs, nil
}
