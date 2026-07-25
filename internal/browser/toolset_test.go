package browser

import (
	"context"
	"encoding/base64"
	"strings"
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
	calls     []toolsetCall
	result    string
	resultSet bool
}

func (c *toolsetClient) Call(_ context.Context, scope, action string, params any) (<-chan brokerFrame, error) {
	c.calls = append(c.calls, toolsetCall{scope: scope, action: action, params: params})
	ch := make(chan brokerFrame, 1)
	data := `{"ok":true}`
	if c.resultSet {
		data = c.result
	}
	ch <- brokerFrame{data: data}
	close(ch)
	return ch, nil
}

func (c *toolsetClient) withResult(r string) *toolsetClient {
	c.result = r
	c.resultSet = true
	return c
}

func (c *toolsetClient) CloseScope(context.Context, string) error {
	return nil
}

type testRuntime struct {
	tmpPath string
}

func (rt testRuntime) Truncate(_ context.Context, text string, _ int) string { return text }
func (rt testRuntime) Execute(_ context.Context, _ string) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}
func (rt testRuntime) Spawn(_ context.Context, _ string) (*runtime.ProcessHandle, error) {
	return nil, nil
}
func (rt testRuntime) ReadRawBytes(_ context.Context, _ string) ([]byte, error) { return nil, nil }
func (rt testRuntime) ReadFile(_ context.Context, _ string, _, _ int) (runtime.ReadFileResult, error) {
	return runtime.ReadFileResult{}, nil
}
func (rt testRuntime) WriteFile(_ context.Context, _, _ string) error { return nil }
func (rt testRuntime) WriteTmpFile(_ context.Context, _ string) (string, error) {
	return rt.tmpPath, nil
}
func (rt testRuntime) AppendFile(_ context.Context, _, _ string) error { return nil }
func (rt testRuntime) Glob(_ context.Context, _ string) (runtime.GlobResult, error) {
	return runtime.GlobResult{}, nil
}
func (rt testRuntime) EditFile(_ context.Context, _ string, _ []runtime.Edit) error { return nil }
func (rt testRuntime) OSInfo(_ context.Context) (string, error)                     { return "", nil }

func newTestClient(caller BrokerCaller) Client {
	return newClient(caller, testRuntime{tmpPath: "tmp/screenshot-test.png"}, 0)
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

func TestToolsetScreenshotRegistered(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	handler, ok := registry.Handler("browser_screenshot")
	if !ok {
		t.Fatal("browser_screenshot was not registered")
	}
	if len(client.calls) != 0 {
		t.Fatal("expected no broker calls before invocation")
	}
	_ = handler
}

func TestToolsetScreenshotRejectsEmptyTab(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	handler, ok := registry.Handler("browser_screenshot")
	if !ok {
		t.Fatal("browser_screenshot was not registered")
	}
	result, err := handler(context.Background(), []byte(`{"selector":"#main"}`))
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !strings.HasPrefix(result.Text, "error:") {
		t.Fatalf("expected error result, got: %s", result.Text)
	}
	if len(client.calls) != 0 {
		t.Fatal("expected no broker calls")
	}
}

func TestToolsetScreenshotSavesToPath(t *testing.T) {
	pngData := base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	client := (&toolsetClient{}).withResult(pngData)
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	handler, ok := registry.Handler("browser_screenshot")
	if !ok {
		t.Fatal("browser_screenshot was not registered")
	}
	result, err := handler(context.Background(), []byte(`{"tab":"tab-1","full_page":true}`))
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected 1 broker call, got %d", len(client.calls))
	}
	call := client.calls[0]
	if call.action != "screenshot" {
		t.Fatalf("expected action 'screenshot', got %q", call.action)
	}
	if !strings.Contains(result.Text, "bytes") {
		t.Fatalf("expected bytes in result, got: %s", result.Text)
	}
}

func TestToolsetScreenshotTmpFileDefault(t *testing.T) {
	pngData := base64.StdEncoding.EncodeToString([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A})
	client := (&toolsetClient{}).withResult(pngData)
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	handler, ok := registry.Handler("browser_screenshot")
	if !ok {
		t.Fatal("browser_screenshot was not registered")
	}
	result, err := handler(context.Background(), []byte(`{"tab":"tab-1"}`))
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !strings.Contains(result.Text, "screenshot-test.png") {
		t.Fatalf("expected tmp path in result, got: %s", result.Text)
	}
}

func TestToolsetScreenshotRejectsEmptyData(t *testing.T) {
	client := (&toolsetClient{}).withResult("")
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	handler, ok := registry.Handler("browser_screenshot")
	if !ok {
		t.Fatal("browser_screenshot was not registered")
	}
	result, err := handler(context.Background(), []byte(`{"tab":"tab-1"}`))
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if !strings.HasPrefix(result.Text, "error:") {
		t.Fatalf("expected error result, got: %s", result.Text)
	}
}
