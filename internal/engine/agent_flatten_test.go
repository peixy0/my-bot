package engine

import (
	"strings"
	"testing"

	"my-bot/internal/llm"
)

func TestFlattenMessages_UserAssistantOnly(t *testing.T) {
	messages := []llm.ChatMessage{
		llm.UserMessage("hello"),
		llm.AssistantMessage("hi there", "", nil),
	}
	got := flattenMessages(messages, 0)
	if !strings.Contains(got, "[USER]: hello") {
		t.Fatalf("missing [USER]: hello in %q", got)
	}
	if !strings.Contains(got, "[ASSISTANT]: hi there") {
		t.Fatalf("missing [ASSISTANT]: hi there in %q", got)
	}
}

func TestFlattenMessages_ToolResultMerged(t *testing.T) {
	messages := []llm.ChatMessage{
		llm.UserMessage("read a file"),
		{
			Role:      "assistant",
			Content:   "let me read it",
			ToolCalls: []llm.ToolCallInMsg{{ID: "call_1", Type: "function", Function: llm.ToolCallFunc{Name: "read_file", Arguments: `{"filename":"test.txt"}`}}},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "file contents here"},
	}
	got := flattenMessages(messages, 0)
	if !strings.Contains(got, `[TOOL read_file({"filename":"test.txt"})]: file contents here`) {
		t.Fatalf("expected tool result merged after tool call, got %q", got)
	}
	// tool result should NOT appear as a standalone line
	if strings.Contains(got, "[tool]:") {
		t.Fatalf("tool result should not appear as standalone line in %q", got)
	}
}

func TestFlattenMessages_ToolResultTruncated(t *testing.T) {
	longResult := strings.Repeat("x", 500)
	messages := []llm.ChatMessage{
		llm.UserMessage("run command"),
		{
			Role:      "assistant",
			Content:   "",
			ToolCalls: []llm.ToolCallInMsg{{ID: "call_1", Type: "function", Function: llm.ToolCallFunc{Name: "run_command", Arguments: `{}`}}},
		},
		{Role: "tool", ToolCallID: "call_1", Content: longResult},
	}
	got := flattenMessages(messages, 100)
	expected := strings.Repeat("x", 100) + "..."
	if !strings.Contains(got, expected) {
		t.Fatalf("expected truncated result %q in output, got %q", expected, truncateStr(got, 200))
	}
	// full result should not appear
	if strings.Contains(got, strings.Repeat("x", 101)) {
		t.Fatalf("result should be truncated to 100 chars in %q", truncateStr(got, 200))
	}
}

func TestFlattenMessages_TruncateZeroNoTruncation(t *testing.T) {
	longResult := strings.Repeat("y", 300)
	messages := []llm.ChatMessage{
		llm.UserMessage("go"),
		{
			Role:      "assistant",
			Content:   "",
			ToolCalls: []llm.ToolCallInMsg{{ID: "c1", Type: "function", Function: llm.ToolCallFunc{Name: "run", Arguments: `{}`}}},
		},
		{Role: "tool", ToolCallID: "c1", Content: longResult},
	}
	got := flattenMessages(messages, 0)
	if !strings.Contains(got, longResult) {
		t.Fatalf("expected full result with truncate=0, got %q", truncateStr(got, 200))
	}
}

func TestFlattenMessages_ToolResultMissing(t *testing.T) {
	messages := []llm.ChatMessage{
		llm.UserMessage("do something"),
		{
			Role:      "assistant",
			Content:   "calling tool",
			ToolCalls: []llm.ToolCallInMsg{{ID: "call_99", Type: "function", Function: llm.ToolCallFunc{Name: "read_file", Arguments: `{"filename":"x"}`}}},
		},
		// no tool result — simulates retained assistant with pending toolMsgs
	}
	got := flattenMessages(messages, 0)
	if !strings.Contains(got, `[TOOL read_file({"filename":"x"})]: `) {
		// result should be empty string after the colon-space
		t.Fatalf("expected empty tool result for missing tool_call_id, got %q", got)
	}
}

func TestFlattenMessages_MultipleToolCallsOneAssistant(t *testing.T) {
	messages := []llm.ChatMessage{
		llm.UserMessage("multi"),
		{
			Role:    "assistant",
			Content: "doing two things",
			ToolCalls: []llm.ToolCallInMsg{
				{ID: "c1", Type: "function", Function: llm.ToolCallFunc{Name: "read_file", Arguments: `{"filename":"a"}`}},
				{ID: "c2", Type: "function", Function: llm.ToolCallFunc{Name: "run_command", Arguments: `{"command":"ls"}`}},
			},
		},
		{Role: "tool", ToolCallID: "c1", Content: "content a"},
		{Role: "tool", ToolCallID: "c2", Content: "output ls"},
	}
	got := flattenMessages(messages, 0)
	if !strings.Contains(got, `[TOOL read_file({"filename":"a"})]: content a`) {
		t.Fatalf("missing first tool result in %q", got)
	}
	if !strings.Contains(got, `[TOOL run_command({"command":"ls"})]: output ls`) {
		t.Fatalf("missing second tool result in %q", got)
	}
}

func TestFlattenMessages_ContextAnchor(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "user", Content: "[CONTEXT ANCHOR]\nold summary"},
		llm.UserMessage("new task"),
	}
	got := flattenMessages(messages, 0)
	if !strings.Contains(got, "[USER]: [CONTEXT ANCHOR]\nold summary") {
		t.Fatalf("expected anchor as [USER] in %q", got)
	}
	if !strings.Contains(got, "[USER]: new task") {
		t.Fatalf("expected new task as [USER] in %q", got)
	}
}

func TestFlattenMessages_MultimodalContent(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "user", Content: []map[string]any{
			{"type": "text", "text": "look at this"},
			{"type": "image_url", "image_url": map[string]any{"url": "http://example.com/img.png"}},
		}},
	}
	got := flattenMessages(messages, 0)
	if !strings.Contains(got, "look at this") {
		t.Fatalf("expected text content in %q", got)
	}
	if !strings.Contains(got, "[image_url]") {
		t.Fatalf("expected [image_url] placeholder in %q", got)
	}
}

func TestFindLastGroupStart_AssistantWithToolResults(t *testing.T) {
	messages := []llm.ChatMessage{
		llm.UserMessage("q"),
		{Role: "assistant", ToolCalls: []llm.ToolCallInMsg{{ID: "c1"}}},
		{Role: "tool", ToolCallID: "c1"},
		{Role: "tool", ToolCallID: "c1"},
	}
	// last group = assistant at index 1 + its two tool results
	idx := findLastGroupStart(messages)
	if idx != 1 {
		t.Fatalf("expected last group start at 1, got %d", idx)
	}
}

func TestFindLastGroupStart_UserMessage(t *testing.T) {
	messages := []llm.ChatMessage{
		{Role: "assistant", Content: "reply"},
		llm.UserMessage("new question"),
	}
	idx := findLastGroupStart(messages)
	if idx != 1 {
		t.Fatalf("expected last group start at 1, got %d", idx)
	}
}

func TestFindLastGroupStart_AssistantNoToolCalls(t *testing.T) {
	messages := []llm.ChatMessage{
		llm.UserMessage("hi"),
		{Role: "assistant", Content: "hello"},
	}
	idx := findLastGroupStart(messages)
	if idx != 1 {
		t.Fatalf("expected last group start at 1, got %d", idx)
	}
}
