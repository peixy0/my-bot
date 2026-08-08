package browser

import (
	"context"
	"encoding/base64"
	"slices"
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
func (rt testRuntime) TruncateTail(_ context.Context, text string, _ int) string {
	return text
}
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
func (rt testRuntime) Glob(_ context.Context, _ string, _ int) (runtime.GlobResult, error) {
	return runtime.GlobResult{}, nil
}
func (rt testRuntime) EditFile(_ context.Context, _ string, _ []runtime.Edit) error { return nil }
func (rt testRuntime) OSInfo(_ context.Context) (string, error)                     { return "", nil }

func newTestClient(caller BrokerCaller) Client {
	return newClient(caller, testRuntime{tmpPath: "tmp/screenshot-test.png"}, 0)
}

func executeTool(t *testing.T, registry *tools.Registry, name, args string) tools.ToolResult {
	t.Helper()
	prepared, err := prepareRegisteredTool(t, registry, name, args)
	if err != nil {
		t.Fatalf("%s unexpected preparation error: %v", name, err)
	}
	result, err := prepared.Execute(context.Background())
	if err != nil {
		t.Fatalf("%s unexpected execution error: %v", name, err)
	}
	return result
}

func prepareRegisteredTool(t *testing.T, registry *tools.Registry, name, args string) (tools.PreparedTool, error) {
	t.Helper()
	preparer, ok := registry.Get(name)
	if !ok {
		t.Fatalf("%s was not registered", name)
	}
	return preparer([]byte(args))
}

func TestToolsetRegistersAllBrowserTools(t *testing.T) {
	registry := tools.NewRegistry()
	newTestClient(&toolsetClient{}).Register(registry)

	want := []string{
		"browser_click",
		"browser_close_tab",
		"browser_evaluate",
		"browser_inspect",
		"browser_navigate",
		"browser_network",
		"browser_press_key",
		"browser_screenshot",
		"browser_scroll",
		"browser_set_value",
		"browser_snapshot",
		"browser_tabs",
		"browser_wait",
	}
	got := registry.Names()
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected browser tools:\ngot:  %v\nwant: %v", got, want)
	}
}

func TestToolsetParameterErrorsMatchDefaultToolsetStyle(t *testing.T) {
	registry := tools.NewRegistry()
	newTestClient(&toolsetClient{}).Register(registry)

	tests := []struct {
		name string
		args string
		want string
	}{
		{name: "browser_tabs", args: `{`, want: "parse browser_tabs args:"},
		{name: "browser_navigate", args: `{"url":"https://example.com"}`, want: "browser_navigate tab must not be empty"},
		{name: "browser_navigate", args: `{"tab":"tab-1"}`, want: "browser_navigate url must not be empty"},
		{name: "browser_evaluate", args: `{"tab":"tab-1"}`, want: "browser_evaluate script must not be empty"},
		{name: "browser_wait", args: `{"tab":"tab-1","seconds":31}`, want: "browser_wait seconds must be greater than 0 and at most 30"},
		{name: "browser_network", args: `{"tab":"tab-1","action":"detail"}`, want: "browser_network request_id must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name+"_"+tt.want, func(t *testing.T) {
			_, err := prepareRegisteredTool(t, registry, tt.name, tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestToolsetDescriptionsOmitTabDetails(t *testing.T) {
	registry := tools.NewRegistry()
	newTestClient(&toolsetClient{}).Register(registry)

	tests := []struct {
		name string
		args string
		want string
	}{
		{name: "browser_click", args: `{"tab":"tab-1","element_ref":"42"}`, want: "Clicking in browser"},
		{name: "browser_press_key", args: `{"tab":"tab-1","key":"Enter"}`, want: "Pressing keys in browser"},
		{name: "browser_navigate", args: `{"tab":"tab-1","url":"https://example.com/path"}`, want: `Navigating to https://example.com/path in browser`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prepared, err := prepareRegisteredTool(t, registry, tt.name, tt.args)
			if err != nil {
				t.Fatalf("prepare %s: %v", tt.name, err)
			}
			if prepared.Description != tt.want {
				t.Fatalf("unexpected description: got %q, want %q", prepared.Description, tt.want)
			}
			if strings.Contains(prepared.Description, "tab-1") {
				t.Fatalf("description leaked tab detail: %q", prepared.Description)
			}
		})
	}
}

func TestToolsetRoutesOwnedScope(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	result := executeTool(t, registry, "browser_evaluate", `{"tab":"tab-1","script":"location.href"}`)
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

	_, err := prepareRegisteredTool(t, registry, "browser_evaluate", `{"tab":"tab-1","script":""}`)
	if err == nil {
		t.Fatal("expected preparation error")
	}
	if len(client.calls) != 0 {
		t.Fatal("expected no broker calls")
	}
}

func TestToolsetScrollRejectsBadDirection(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	_, err := prepareRegisteredTool(t, registry, "browser_scroll", `{"tab":"tab-1","direction":"left"}`)
	if err == nil {
		t.Fatal("expected preparation error")
	}
	if len(client.calls) != 0 {
		t.Fatal("expected no broker calls")
	}
}

func TestToolsetScrollDefaultsAmount(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	result := executeTool(t, registry, "browser_scroll", `{"tab":"tab-1","direction":"down"}`)
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
	params, ok := call.params.(*scrollParams)
	if !ok {
		t.Fatalf("expected scroll params, got %T", call.params)
	}
	if params.Amount != 500 {
		t.Fatalf("expected default amount 500, got %.0f", params.Amount)
	}
}

func TestToolsetBackForwardRegistered(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	for _, action := range []string{"back", "forward", "reload"} {
		result := executeTool(t, registry, "browser_navigate", `{"tab":"tab-1","action":"`+action+`"}`)
		if result.Text != `{"ok":true}` {
			t.Fatalf("%s unexpected result: %s", action, result.Text)
		}
	}
	if len(client.calls) != 3 {
		t.Fatalf("expected 3 broker calls, got %d", len(client.calls))
	}
	for i, wantAction := range []string{"back", "forward", "reload"} {
		if client.calls[i].action != wantAction {
			t.Fatalf("call %d: expected action %q, got %q", i, wantAction, client.calls[i].action)
		}
	}
}

func TestToolsetRejectsLongWait(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	_, err := prepareRegisteredTool(t, registry, "browser_wait", `{"seconds":31}`)
	if err == nil {
		t.Fatal("expected preparation error")
	}
	if len(client.calls) != 0 {
		t.Fatal("expected no broker calls")
	}
}

func TestToolsetScreenshotRegistered(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	_, err := prepareRegisteredTool(t, registry, "browser_screenshot", `{"tab":"tab-1"}`)
	if err != nil {
		t.Fatalf("unexpected preparation error: %v", err)
	}
	if len(client.calls) != 0 {
		t.Fatal("expected no broker calls before invocation")
	}
}

func TestToolsetScreenshotRejectsEmptyTab(t *testing.T) {
	client := &toolsetClient{}
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	_, err := prepareRegisteredTool(t, registry, "browser_screenshot", `{"selector":"#main"}`)
	if err == nil {
		t.Fatal("expected preparation error")
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

	result := executeTool(t, registry, "browser_screenshot", `{"tab":"tab-1","full_page":true}`)
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

	result := executeTool(t, registry, "browser_screenshot", `{"tab":"tab-1"}`)
	if !strings.Contains(result.Text, "screenshot-test.png") {
		t.Fatalf("expected tmp path in result, got: %s", result.Text)
	}
}

func TestToolsetScreenshotRejectsEmptyData(t *testing.T) {
	client := (&toolsetClient{}).withResult("")
	registry := tools.NewRegistry()
	newTestClient(client).Register(registry)

	result := executeTool(t, registry, "browser_screenshot", `{"tab":"tab-1"}`)
	if !strings.HasPrefix(result.Text, "error:") {
		t.Fatalf("expected error result, got: %s", result.Text)
	}
}
