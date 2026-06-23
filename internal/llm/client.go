package llm

import (
	"context"
)

type ChatMessage struct {
	Role             string          `json:"role"`
	Content          any             `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCallInMsg `json:"tool_calls,omitempty"`
	ToolCallID       string          `json:"tool_call_id,omitempty"`
}

type ToolCallInMsg struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolCallFunc `json:"function"`
}

type ToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ContentPart = map[string]any

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
	Messages       []ChatMessage
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
	Messages    []ChatMessage
	TotalTokens int64
}

func NewConversation() *Conversation {
	return &Conversation{}
}

func userMessage(content string) ChatMessage {
	return ChatMessage{Role: "user", Content: content}
}

func assistantMessage(content, reasoningContent string, toolCalls []ToolCall) ChatMessage {
	msg := ChatMessage{Role: "assistant"}
	if content != "" {
		msg.Content = content
	}
	if reasoningContent != "" {
		msg.ReasoningContent = reasoningContent
	}
	if len(toolCalls) > 0 {
		calls := make([]ToolCallInMsg, len(toolCalls))
		for i, tc := range toolCalls {
			calls[i] = ToolCallInMsg{
				ID:   tc.ID,
				Type: "function",
				Function: ToolCallFunc{
					Name:      tc.Name,
					Arguments: string(tc.Args),
				},
			}
		}
		msg.ToolCalls = calls
	}
	return msg
}

func toolResultMessage(callID, content string) ChatMessage {
	return ChatMessage{
		Role:       "tool",
		ToolCallID: callID,
		Content:    content,
	}
}

func userBlocksMessage(text string, blocks []ContentPart) ChatMessage {
	parts := make([]ContentPart, 0, len(blocks)+1)
	if text != "" {
		parts = append(parts, ContentPart{"type": "text", "text": text})
	}
	parts = append(parts, blocks...)
	return ChatMessage{Role: "user", Content: parts}
}
