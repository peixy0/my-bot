package browser

import (
	"context"
	"encoding/json"
	"testing"

	"my-bot/internal/runtime"
	"my-bot/internal/tools"
)

type toolsetCall struct {
	scope  string
	action string
	params any
}

type toolsetClient struct {
	calls []toolsetCall
}

func (c *toolsetClient) Call(_ context.Context, scope, action string, params any) (json.RawMessage, error) {
	c.calls = append(c.calls, toolsetCall{scope: scope, action: action, params: params})
	return json.RawMessage(`{"ok":true}`), nil
}

func (c *toolsetClient) CloseScope(context.Context, string) error {
	return nil
}

type testRuntime struct{}

func (testRuntime) Truncate(_ context.Context, text string, _ int) string { return text }
func (testRuntime) Execute(_ context.Context, _ string) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}
func (testRuntime) Spawn(_ context.Context, _ string) (*runtime.ProcessHandle, error) {
	return nil, nil
}
func (testRuntime) ReadRawBytes(_ context.Context, _ string) ([]byte, error) { return nil, nil }
func (testRuntime) ReadFile(_ context.Context, _ string, _, _ int) (runtime.ReadFileResult, error) {
	return runtime.ReadFileResult{}, nil
}
func (testRuntime) WriteFile(_ context.Context, _, _ string) error           { return nil }
func (testRuntime) WriteTmpFile(_ context.Context, _ string) (string, error) { return "", nil }
func (testRuntime) AppendFile(_ context.Context, _, _ string) error          { return nil }
func (testRuntime) Glob(_ context.Context, _ string) (runtime.GlobResult, error) {
	return runtime.GlobResult{}, nil
}
func (testRuntime) EditFile(_ context.Context, _ string, _ []runtime.Edit) error { return nil }
func (testRuntime) OSInfo(_ context.Context) (string, error)                     { return "", nil }

func newTestClient(caller BrokerCaller) Client {
	return newClient(caller, testRuntime{}, 0)
}

func TestToolsetRoutesOwnedScope(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	handler, ok := registry.Handler("browser_evaluate")
	if !ok {
		t.Fatal("browser_evaluate was not registered")
	}
	result, err := handler(context.Background(), []byte(`{"tab":"tab-1","script":"location.href"}`))
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result.Text != `{"ok":true}` {
		t.Fatalf("unexpected result: %s", result.Text)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected one broker call, got %d", len(client.calls))
	}
	call := client.calls[0]
	if call.scope == "" || call.action != "evaluate" {
		t.Fatalf("unexpected call: %#v", call)
	}
}

func TestToolsetRejectsEmptyScript(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	handler, ok := registry.Handler("browser_evaluate")
	if !ok {
		t.Fatal("browser_evaluate was not registered")
	}
	result, err := handler(context.Background(), []byte(`{"script":" "}`))
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected tool error result")
	}
	if len(client.calls) != 0 {
		t.Fatal("expected no broker calls")
	}
}

func TestToolsetScrollRejectsBadDirection(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	handler, ok := registry.Handler("browser_scroll")
	if !ok {
		t.Fatal("browser_scroll was not registered")
	}
	result, err := handler(context.Background(), []byte(`{"tab":"tab-1","direction":"left"}`))
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected tool error result")
	}
	if len(client.calls) != 0 {
		t.Fatal("expected no broker calls")
	}
}

func TestToolsetScrollDefaultsAmount(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	handler, ok := registry.Handler("browser_scroll")
	if !ok {
		t.Fatal("browser_scroll was not registered")
	}
	result, err := handler(context.Background(), []byte(`{"tab":"tab-1","direction":"down"}`))
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result.Text != `{"ok":true}` {
		t.Fatalf("unexpected result: %s", result.Text)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected one broker call, got %d", len(client.calls))
	}
	call := client.calls[0]
	if call.action != "scroll" {
		t.Fatalf("expected action 'scroll', got %q", call.action)
	}
}

func TestToolsetBackForwardRegistered(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	for _, name := range []string{"browser_back", "browser_forward", "browser_reload"} {
		handler, ok := registry.Handler(name)
		if !ok {
			t.Fatalf("%s was not registered", name)
		}
		result, err := handler(context.Background(), []byte(`{"tab":"tab-1"}`))
		if err != nil {
			t.Fatalf("%s unexpected handler error: %v", name, err)
		}
		if result.Text != `{"ok":true}` {
			t.Fatalf("%s unexpected result: %s", name, result.Text)
		}
	}
	if len(client.calls) != 3 {
		t.Fatalf("expected 3 broker calls, got %d", len(client.calls))
	}
}

func TestToolsetRejectsLongWait(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	handler, ok := registry.Handler("browser_wait")
	if !ok {
		t.Fatal("browser_wait was not registered")
	}
	result, err := handler(context.Background(), []byte(`{"seconds":31}`))
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result.Text == "" {
		t.Fatal("expected tool error result")
	}
	if len(client.calls) != 0 {
		t.Fatal("expected no broker calls")
	}
}
