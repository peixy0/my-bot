package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOpenAIProvider_CompleteStreamsContent(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("expected authorization header, got %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "Hel"}}}})
		writeSSE(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "lo"}, "finish_reason": "stop"}}})
		writeSSE(t, w, map[string]any{"choices": []any{}, "usage": map[string]any{"total_tokens": 42}})
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider := NewOpenAIProvider(server.URL, "test-key", server.Client())
	var deltas []string
	resp, err := provider.Complete(context.Background(), CompletionRequest{
		Model:    "test-model",
		Messages: []Message{userMessage("hi")},
		OnContentDelta: func(_ context.Context, delta string) {
			deltas = append(deltas, delta)
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "Hello" || resp.FinishReason != "stop" || resp.TotalTokens != 42 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if !reflect.DeepEqual(deltas, []string{"Hel", "lo"}) {
		t.Fatalf("expected content deltas, got %v", deltas)
	}
	if requestBody["stream"] != true {
		t.Fatalf("expected stream=true in request, got %#v", requestBody["stream"])
	}
	streamOptions, _ := requestBody["stream_options"].(map[string]any)
	if streamOptions["include_usage"] != true {
		t.Fatalf("expected include_usage=true, got %#v", requestBody["stream_options"])
	}
}

func TestOpenAIProvider_CompleteStreamsToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, map[string]any{
			"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{
				"index": 0,
				"id":    "call_1",
				"function": map[string]any{
					"name":      "lookup",
					"arguments": `{"q"`,
				},
			}}}}},
		})
		writeSSE(t, w, map[string]any{
			"choices": []any{map[string]any{
				"delta": map[string]any{"tool_calls": []any{map[string]any{
					"index":    0,
					"function": map[string]any{"arguments": `:"go"}`},
				}}},
				"finish_reason": "tool_calls",
			}},
		})
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider := NewOpenAIProvider(server.URL, "", server.Client())
	resp, err := provider.Complete(context.Background(), CompletionRequest{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Fatalf("expected tool_calls finish, got %q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %+v", resp.ToolCalls)
	}
	call := resp.ToolCalls[0]
	if call.ID != "call_1" || call.Name != "lookup" || string(call.Args) != `{"q":"go"}` {
		t.Fatalf("unexpected tool call: %+v args=%s", call, call.Args)
	}
}

func TestOpenAIProvider_CompleteStreamsReasoningContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": "step "}}}})
		writeSSE(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning_content": "one", "content": "answer"}, "finish_reason": "stop"}}})
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider := NewOpenAIProvider(server.URL, "", server.Client())
	resp, err := provider.Complete(context.Background(), CompletionRequest{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ReasoningContent != "step one" {
		t.Fatalf("expected reasoning_content to be aggregated, got %q", resp.ReasoningContent)
	}
	if resp.Content != "answer" {
		t.Fatalf("expected answer content, got %q", resp.Content)
	}
}

func TestOpenAIProvider_CompletePreservesThinkTagsFromStreamedContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSE(t, w, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "<think>hidden</think> visible"}, "finish_reason": "stop"}}})
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	provider := NewOpenAIProvider(server.URL, "", server.Client())
	resp, err := provider.Complete(context.Background(), CompletionRequest{Model: "m"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "<think>hidden</think> visible" {
		t.Fatalf("expected think tag preserved, got %q", resp.Content)
	}
}

func TestOpenAIProvider_CompleteReturnsAPIErrorForNonOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	provider := NewOpenAIProvider(server.URL, "", server.Client())
	_, err := provider.Complete(context.Background(), CompletionRequest{Model: "m"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest || !strings.Contains(apiErr.Message, "bad request") {
		t.Fatalf("expected 400 APIError, got %v", err)
	}
}

func TestOpenAIProvider_CompleteRejectsMalformedSSEJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {not-json}\n\n")
	}))
	defer server.Close()

	provider := NewOpenAIProvider(server.URL, "", server.Client())
	_, err := provider.Complete(context.Background(), CompletionRequest{Model: "m"})
	if err == nil || !strings.Contains(err.Error(), "unmarshal stream chunk") {
		t.Fatalf("expected malformed stream error, got %v", err)
	}
}

func TestParseChatCompletionStream_RejectsEOFBeforeDone(t *testing.T) {
	stream := strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"stop\"}]}\n\n")

	_, err := parseChatCompletionStream(context.Background(), stream, nil)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected unexpected EOF, got %v", err)
	}
}

func TestParseChatCompletionStream_PreservesThinkTagsInDeltas(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hello <thi"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"nk>hidden</th"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"ink> visible<"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":" end"},"finish_reason":"stop"}]}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n"))

	var deltas []string
	resp, err := parseChatCompletionStream(context.Background(), stream, func(_ context.Context, delta string) {
		deltas = append(deltas, delta)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "hello <think>hidden</think> visible< end" {
		t.Fatalf("unexpected final content: %q", resp.Content)
	}
	if got := strings.Join(deltas, ""); got != resp.Content {
		t.Fatalf("expected streamed content %q, got %q via %v", resp.Content, got, deltas)
	}
}

func TestIsRetryable_UnexpectedEOF(t *testing.T) {
	if !isRetryable(io.ErrUnexpectedEOF) {
		t.Fatal("expected unexpected EOF to be retryable")
	}
}

func TestRetryExponential_RetriesOnUnexpectedEOF(t *testing.T) {
	originalRetryAfter := retryAfter
	retryAfter = func(time.Duration) <-chan time.Time {
		ch := make(chan time.Time, 1)
		ch <- time.Now()
		return ch
	}
	defer func() { retryAfter = originalRetryAfter }()

	calls := 0
	err := retryExponential(context.Background(), 2, func() error {
		calls++
		if calls == 1 {
			return io.ErrUnexpectedEOF
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected retry after unexpected EOF, got %d calls", calls)
	}
}

func writeSSE(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal SSE: %v", err)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

func TestRetryExponential_SucceedsOnFirstTry(t *testing.T) {
	calls := 0
	err := retryExponential(context.Background(), 5, func() error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestRetryExponential_NonRetryableError(t *testing.T) {
	apiErr := &APIError{StatusCode: 400, Message: "bad request"}
	calls := 0
	err := retryExponential(context.Background(), 5, func() error {
		calls++
		return apiErr
	})
	if !errors.Is(err, apiErr) {
		t.Fatalf("expected apiErr, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call (non-retryable), got %d", calls)
	}
}

func TestRetryExponential_RetriesOnServerError(t *testing.T) {
	calls := 0
	err := retryExponential(context.Background(), 3, func() error {
		calls++
		if calls < 3 {
			return &APIError{StatusCode: 500, Message: "server error"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestRetryExponential_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls := 0
	err := retryExponential(ctx, 99, func() error {
		calls++
		return errors.New("transient")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls > 2 {
		t.Errorf("expected at most 2 calls with cancelled ctx, got %d", calls)
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		err       error
		retryable bool
	}{
		{&APIError{StatusCode: 400, Message: ""}, false},
		{&APIError{StatusCode: 401, Message: ""}, false},
		{&APIError{StatusCode: 429, Message: ""}, true},
		{&APIError{StatusCode: 500, Message: ""}, true},
		{&APIError{StatusCode: 503, Message: ""}, true},
		{errors.New("network timeout"), false},
	}

	for _, tt := range tests {
		got := isRetryable(tt.err)
		if got != tt.retryable {
			t.Errorf("isRetryable(%v) = %v, want %v", tt.err, got, tt.retryable)
		}
	}
}
