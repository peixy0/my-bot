package engine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"my-bot/internal/events"
	"my-bot/internal/inbox"
	"my-bot/internal/llm"
	"my-bot/internal/tasks"

	"golang.org/x/sync/errgroup"
)

type Orchestrator interface {
	OnContentBegin(context.Context)
	OnContentDelta(context.Context, string)
	OnContentFinal(context.Context, *events.ResponseMetadata)
	OnFinalResponse(context.Context, string, *events.ResponseMetadata)
	BeforeToolUse(context.Context, string, []string)
	DispatchTools(context.Context, []preparedToolCall) ([]llm.CallOutcome, error)
	MaybeInterrupt(context.Context) *llm.ChatMessage
}

type HumanInputOrchestrator struct {
	sender        events.Outbound
	inLoopInbox   inbox.Inbox[events.MessageEvent]
	visionSupport bool
}

func NewHumanInputOrchestrator(sender events.Outbound, inLoopInbox inbox.Inbox[events.MessageEvent]) *HumanInputOrchestrator {
	return &HumanInputOrchestrator{sender: sender, inLoopInbox: inLoopInbox}
}

func (o *HumanInputOrchestrator) WithVision(visionSupport bool) *HumanInputOrchestrator {
	o.visionSupport = visionSupport
	return o
}

func (o *HumanInputOrchestrator) OnContentBegin(ctx context.Context) {
	if o.sender != nil {
		o.sender.SendBegin(ctx)
	}
}

func (o *HumanInputOrchestrator) OnContentDelta(ctx context.Context, delta string) {
	if o.sender == nil || delta == "" {
		return
	}
	o.sender.SendDelta(ctx, delta)
}

func (o *HumanInputOrchestrator) OnContentFinal(ctx context.Context, metadata *events.ResponseMetadata) {
	if o.sender == nil {
		return
	}
	o.sender.SendFinal(ctx, metadata)
}

func (o *HumanInputOrchestrator) OnFinalResponse(context.Context, string, *events.ResponseMetadata) {}

func (o *HumanInputOrchestrator) BeforeToolUse(ctx context.Context, content string, descriptions []string) {
	if o.sender == nil || len(descriptions) == 0 {
		return
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		o.sender.SendDelta(ctx, "\n")
	}
	o.sender.SendDelta(ctx, strings.Join(descriptions, "\n"))
}

func (o *HumanInputOrchestrator) DispatchTools(ctx context.Context, calls []preparedToolCall) ([]llm.CallOutcome, error) {
	return runDispatch(ctx, calls)
}

func (o *HumanInputOrchestrator) MaybeInterrupt(context.Context) *llm.ChatMessage {
	items := drainMessageInbox(o.inLoopInbox)
	if len(items) == 0 {
		return nil
	}
	inject := buildMessageBatch(
		items,
		o.visionSupport,
		"[USER MESSAGE INTERRUPTING YOUR CURRENT WORK]\nThe user sent additional message while you were working. Read carefully and adjust your plan immediately if it changes what you should do next:",
	)
	if inject != nil {
		slog.Debug("in-loop user input injected after tool dispatch")
	}
	return inject
}

type BackgroundOrchestrator struct {
	sender events.Outbound
}

func NewBackgroundOrchestrator(sender events.Outbound) *BackgroundOrchestrator {
	return &BackgroundOrchestrator{sender: sender}
}

func (o *BackgroundOrchestrator) OnContentBegin(context.Context) {}

func (o *BackgroundOrchestrator) OnContentDelta(context.Context, string) {}

func (o *BackgroundOrchestrator) OnContentFinal(context.Context, *events.ResponseMetadata) {}

func (o *BackgroundOrchestrator) OnFinalResponse(ctx context.Context, content string, metadata *events.ResponseMetadata) {
	const noReport = "NO_REPORT"
	if o.sender == nil || content == "" || strings.HasSuffix(strings.TrimSpace(content), noReport) {
		return
	}
	o.sender.SendFull(ctx, content, metadata)
}

func (o *BackgroundOrchestrator) BeforeToolUse(context.Context, string, []string) {}

func (o *BackgroundOrchestrator) DispatchTools(ctx context.Context, calls []preparedToolCall) ([]llm.CallOutcome, error) {
	return runDispatch(ctx, calls)
}

func (o *BackgroundOrchestrator) MaybeInterrupt(context.Context) *llm.ChatMessage {
	return nil
}

type SubagentOrchestrator struct {
	emit  *tasks.Emitter
	input <-chan string
}

func NewSubagentOrchestrator(emit *tasks.Emitter, input <-chan string) *SubagentOrchestrator {
	return &SubagentOrchestrator{emit: emit, input: input}
}

func (o *SubagentOrchestrator) OnContentBegin(context.Context) {}

func (o *SubagentOrchestrator) OnContentDelta(ctx context.Context, delta string) {
	if delta != "" && o.emit != nil {
		o.emit.Output(delta)
	}
}

func (o *SubagentOrchestrator) OnContentFinal(context.Context, *events.ResponseMetadata) {}

func (o *SubagentOrchestrator) OnFinalResponse(context.Context, string, *events.ResponseMetadata) {}

func (o *SubagentOrchestrator) BeforeToolUse(context.Context, string, []string) {}

func (o *SubagentOrchestrator) DispatchTools(ctx context.Context, calls []preparedToolCall) ([]llm.CallOutcome, error) {
	return runDispatch(ctx, calls)
}

func (o *SubagentOrchestrator) MaybeInterrupt(context.Context) *llm.ChatMessage {
	return o.drainInput()
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

func execOne(ctx context.Context, call preparedToolCall) llm.CallOutcome {
	if call.err != nil {
		slog.Debug("tool call prepare error", "tool", call.call.Name, "id", call.call.ID, "err", call.err)
		return llm.CallOutcome{Err: call.err}
	}
	slog.Debug("tool call start", "tool", call.call.Name, "id", call.call.ID, "args", string(call.call.Args))
	result, err := call.tool.Execute(ctx)
	if err != nil {
		slog.Debug("tool call error", "tool", call.call.Name, "id", call.call.ID, "err", err)
		return llm.CallOutcome{Err: err}
	}
	slog.Debug("tool call done", "tool", call.call.Name, "id", call.call.ID, "result", result.Text)
	if result.Blocks != nil {
		toolText := result.Text
		if strings.TrimSpace(toolText) == "" {
			toolText = fmt.Sprintf("%s returned non-text content. A follow-up user message contains the multimodal payload.", call.call.Name)
		}
		followup := llm.UserBlocksMessage(
			fmt.Sprintf("[TOOL OUTPUT FROM %s]\nInspect the attached content and continue with the task.", call.call.ID),
			result.Blocks,
		)
		return llm.CallOutcome{
			ToolMsg:  llm.ToolResultMessage(call.call.ID, toolText),
			Followup: &followup,
		}
	}
	return llm.CallOutcome{ToolMsg: llm.ToolResultMessage(call.call.ID, result.Text)}
}

func runDispatch(ctx context.Context, calls []preparedToolCall) ([]llm.CallOutcome, error) {
	results := make([]llm.CallOutcome, len(calls))

	if len(calls) == 1 {
		results[0] = execOne(ctx, calls[0])
		return results, nil
	}

	var seqMu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	for i, call := range calls {
		i, call := i, call
		g.Go(func() error {
			if call.parallel {
				results[i] = execOne(gctx, call)
			} else {
				seqMu.Lock()
				results[i] = execOne(gctx, call)
				seqMu.Unlock()
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}
