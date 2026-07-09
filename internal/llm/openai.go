package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"my-bot/internal/util"
)

const maxAttempts = 99
const maxRetryDuration = 1800

var retryAfter = time.After

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api error (status %d): %s", e.StatusCode, e.Message)
}

type OpenAIProvider struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewOpenAIProvider(baseURL, apiKey string, httpClient ...*http.Client) *OpenAIProvider {
	c := http.DefaultClient
	if len(httpClient) > 0 && httpClient[0] != nil {
		c = httpClient[0]
	}
	return &OpenAIProvider{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		httpClient: c,
	}
}

func (p *OpenAIProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	var resp CompletionResponse
	err := retryExponential(ctx, maxAttempts, func() error {
		var err error
		resp, err = p.doComplete(ctx, req)
		return err
	})
	return resp, err
}

func (p *OpenAIProvider) doComplete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	body := map[string]any{
		"model":          req.Model,
		"messages":       req.Messages,
		"temperature":    req.Temperature,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
		body["parallel_tool_calls"] = true
	}
	if req.TopP > 0 {
		body["top_p"] = req.TopP
	}
	if req.TopK > 0 {
		body["top_k"] = req.TopK
	}
	for k, v := range req.ExtraBody {
		if _, exists := body[k]; !exists {
			body[k] = v
		}
	}
	payload, err := util.ToJSON(body)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return CompletionResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return CompletionResponse{}, fmt.Errorf("read error response: %w", err)
		}
		apiErr := &APIError{StatusCode: resp.StatusCode, Message: string(respBody)}
		return CompletionResponse{}, apiErr
	}

	return parseChatCompletionStream(ctx, resp.Body, req.OnContentDelta)
}

type chatCompletionStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content,omitempty"`
			ReasoningContent string `json:"reasoning_content,omitempty"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		TotalTokens int64 `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

type streamAccumulator struct {
	content          strings.Builder
	reasoningContent strings.Builder
	toolCalls        map[int]*toolCallAccumulator
	finishReason     string
	totalTokens      int64
}

type toolCallAccumulator struct {
	id        string
	name      string
	arguments strings.Builder
}

func parseChatCompletionStream(
	ctx context.Context,
	r io.Reader,
	onContentDelta func(context.Context, string),
) (CompletionResponse, error) {
	acc := &streamAccumulator{toolCalls: make(map[int]*toolCallAccumulator)}
	reader := bufio.NewReader(r)

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(line, ":") {
				// SSE comments are keepalives.
			} else if data, ok := strings.CutPrefix(line, "data:"); ok {
				done, err := handleStreamEvent(ctx, data, acc, onContentDelta)
				if done {
					return acc.response(), nil
				}
				if err != nil {
					return CompletionResponse{}, err
				}
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return CompletionResponse{}, io.ErrUnexpectedEOF
			}
			return CompletionResponse{}, err
		}
	}
}

func handleStreamEvent(
	ctx context.Context,
	data string,
	acc *streamAccumulator,
	onContentDelta func(context.Context, string),
) (bool, error) {
	data = strings.TrimSpace(data)
	if data == "" {
		return false, nil
	}
	if data == "[DONE]" {
		return true, nil
	}

	var chunk chatCompletionStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return false, fmt.Errorf("unmarshal stream chunk: %w", err)
	}
	acc.add(ctx, chunk, onContentDelta)
	return false, nil
}

func (a *streamAccumulator) add(
	ctx context.Context,
	chunk chatCompletionStreamChunk,
	onContentDelta func(context.Context, string),
) {
	if chunk.Usage != nil {
		a.totalTokens = chunk.Usage.TotalTokens
	}
	for _, choice := range chunk.Choices {
		if choice.FinishReason != "" {
			a.finishReason = choice.FinishReason
		}
		if choice.Delta.Content != "" {
			a.content.WriteString(choice.Delta.Content)
			if onContentDelta != nil {
				onContentDelta(ctx, choice.Delta.Content)
			}
		}
		if choice.Delta.ReasoningContent != "" {
			a.reasoningContent.WriteString(choice.Delta.ReasoningContent)
		}
		for _, tc := range choice.Delta.ToolCalls {
			call := a.toolCalls[tc.Index]
			if call == nil {
				call = &toolCallAccumulator{}
				a.toolCalls[tc.Index] = call
			}
			if tc.ID != "" {
				call.id = tc.ID
			}
			if tc.Function.Name != "" {
				call.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				call.arguments.WriteString(tc.Function.Arguments)
			}
		}
	}
}

func (a *streamAccumulator) response() CompletionResponse {
	content := strings.TrimSpace(a.content.String())
	indexes := make([]int, 0, len(a.toolCalls))
	for idx := range a.toolCalls {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	calls := make([]ToolCall, 0, len(indexes))
	for _, idx := range indexes {
		tc := a.toolCalls[idx]
		calls = append(calls, ToolCall{
			ID:   tc.id,
			Name: tc.name,
			Args: []byte(tc.arguments.String()),
		})
	}
	finishReason := a.finishReason
	return CompletionResponse{
		Content:          content,
		ReasoningContent: strings.TrimSpace(a.reasoningContent.String()),
		ToolCalls:        calls,
		FinishReason:     finishReason,
		TotalTokens:      a.totalTokens,
	}
}

func isRetryable(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	if apiErr, ok := err.(*APIError); ok {
		return apiErr.StatusCode > 403
	}
	return false
}

func retryExponential(ctx context.Context, maxAttempts int, fn func() error) error {
	var err error
	for i := range maxAttempts {
		if err = fn(); err == nil {
			return nil
		}
		if !isRetryable(err) {
			return err
		}
		wait := min(5*math.Pow(2, float64(i)), maxRetryDuration)
		slog.Warn("retrying after error", "attempt", i+1, "wait_seconds", wait, "err", err)
		select {
		case <-retryAfter(time.Duration(wait) * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}
