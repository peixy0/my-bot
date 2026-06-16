package llm

import (
	"context"
)

type Message = map[string]any

type ToolCall struct {
	ID   string
	Name string
	Args []byte
}

type CompletionResponse struct {
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
	FinishReason     string
	TotalTokens      int64
}

type CompletionRequest struct {
	Model          string
	Messages       []Message
	Tools          []map[string]any
	MaxTokens      int64
	Temperature    float64
	TopP           float64
	TopK           int
	ExtraBody      map[string]any
	OnContentDelta func(ctx context.Context, delta string)
}

type CompletionClient interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}

type Conversation struct {
	Messages    []Message
	TotalTokens int64
}

func NewConversation() *Conversation {
	return &Conversation{}
}

func userMessage(content string) Message {
	return Message{"role": "user", "content": content}
}

func assistantMessage(content, reasoningContent string, toolCalls []ToolCall) Message {
	msg := Message{"role": "assistant"}
	if content != "" {
		msg["content"] = content
	}
	if reasoningContent != "" {
		msg["reasoning_content"] = reasoningContent
	}
	if len(toolCalls) > 0 {
		calls := make([]map[string]any, len(toolCalls))
		for i, tc := range toolCalls {
			calls[i] = map[string]any{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": string(tc.Args),
				},
			}
		}
		msg["tool_calls"] = calls
	}
	return msg
}

func toolResultMessage(callID, content string) Message {
	return Message{
		"role":         "tool",
		"tool_call_id": callID,
		"content":      content,
	}
}

func toolResultBlocksMessage(callID string, blocks []map[string]any) Message {
	return Message{
		"role":         "tool",
		"tool_call_id": callID,
		"content":      blocks,
	}
}
