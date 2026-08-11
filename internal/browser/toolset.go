package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"my-bot/internal/runtime"
	"my-bot/internal/tools"
)

type Client interface {
	tools.Toolset
	Close(context.Context) error
}

type ExtensionClient struct {
	caller   BrokerCaller
	rt       runtime.Runtime
	scopeID  string
	maxChars int
}

type NoopClient struct{}

func NewNoopClient() Client {
	return NoopClient{}
}

func (NoopClient) Close(context.Context) error {
	return nil
}

func (NoopClient) Register(*tools.Registry) {}

func newClient(caller BrokerCaller, rt runtime.Runtime, maxChars int) Client {
	if caller == nil {
		return NewNoopClient()
	}
	return &ExtensionClient{
		caller:   caller,
		rt:       rt,
		scopeID:  uuid.NewString(),
		maxChars: maxChars,
	}
}

func (c *ExtensionClient) Close(ctx context.Context) error {
	return c.caller.CloseScope(ctx, c.scopeID)
}

func (c *ExtensionClient) call(ctx context.Context, action string, params any) (string, error) {
	frames, err := c.caller.Call(ctx, c.scopeID, action, params)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for frame := range frames {
		if frame.err != nil {
			return "", frame.err
		}
		sb.WriteString(frame.data)
	}
	return sb.String(), nil
}

func (c *ExtensionClient) truncate(ctx context.Context, text string) string {
	return c.rt.Truncate(ctx, text, c.maxChars)
}

func (c *ExtensionClient) Register(registry *tools.Registry) {
	c.registerTabs(registry)
	c.registerNavigate(registry)
	c.registerSnapshot(registry)
	c.registerInteraction(registry)
	c.registerEvaluate(registry)
	c.registerInspect(registry)
	c.registerScroll(registry)
	c.registerScreenshot(registry)
	c.registerNetwork(registry)
}

func (c *ExtensionClient) registerTabs(registry *tools.Registry) {
	registry.Register(tools.ToolSchema{
		Name:          "browser_tabs",
		Description:   "List only the browser tabs owned by this agent. Each returned tab may be used for later browser actions. Tab refs persist until the tab is closed.",
		ParameterDesc: map[string]any{"type": "object", "properties": map[string]any{}},
	}, c.prepareTabs)

	registry.Register(tools.ToolSchema{
		Name:        "browser_close_tab",
		Description: "Close an agent-owned browser tab. The tab is detached from CDP debugger and closed. Closing all tabs in a scope destroys that scope.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab"},
			"properties": map[string]any{
				"tab": map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
			},
		},
	}, c.prepareCloseTab)
}

func (c *ExtensionClient) registerNavigate(registry *tools.Registry) {
	registry.Register(tools.ToolSchema{
		Name: "browser_navigate",
		Description: "Navigate a tab: go to a URL, open a new tab, or traverse history.\n\n" +
			"Actions:\n" +
			"• 'go' (default) — navigate tab to url. Requires tab + url.\n" +
			"• 'new' — create a new tab. Requires url.\n" +
			"• 'back' — go back in history. Requires tab.\n" +
			"• 'forward' — go forward in history. Requires tab.\n" +
			"• 'reload' — reload the page. Requires tab.\n\n" +
			"After navigation, call browser_snapshot to confirm the actual URL — SPAs may redirect.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{},
			"properties": map[string]any{
				"tab":    map[string]any{"type": "string", "description": "A tab returned by browser_tabs. Required except when action='new'."},
				"url":    map[string]any{"type": "string", "description": "Absolute URL."},
				"action": map[string]any{"type": "string", "description": "One of: go, new, back, forward, reload. Defaults to 'go'."},
			},
		},
	}, c.prepareNavigate)
}

func (c *ExtensionClient) registerSnapshot(registry *tools.Registry) {
	registry.Register(tools.ToolSchema{
		Name:        "browser_snapshot",
		Description: "Read the accessibility tree of a tab as a nested structure. Every node has a ref (string integer) usable with browser_click and browser_set_value. The tree includes semantic roles (link, button, image, textbox, heading, StaticText, etc.) and accessible names computed by the browser.\n\nIMPORTANT:\n• Element refs are RESET on every snapshot — always snapshot fresh before interacting.\n• SPAs may not expose all content until scrolled. Call browser_scroll first to trigger lazy loading.\n• Nodes omitted: ignored AXTree nodes, nodes beyond the 1000-node cap.\n• Empty fields (name, children) are omitted to keep output compact.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab"},
			"properties": map[string]any{
				"tab": map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
			},
		},
	}, c.prepareSnapshot)
}

func (c *ExtensionClient) registerInteraction(registry *tools.Registry) {
	registry.Register(tools.ToolSchema{
		Name:        "browser_click",
		Description: "Click an element ref from the latest browser_snapshot of an agent-owned tab. Scrolls the element into view first, then clicks via DOM-level HTMLElement.click() for best SPA compatibility, with CDP Input.dispatchMouseEvent as fallback.\n\nNOTE: Element refs become STALE after a new browser_snapshot. Always snapshot then click within the same interaction sequence.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab", "element_ref"},
			"properties": map[string]any{
				"tab":         map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"element_ref": map[string]any{"type": "string", "description": "Element ref returned by browser_snapshot (1-based integer as string, e.g. '42'). Stale refs produce 'element_ref is stale' error — re-snapshot to get fresh refs."},
			},
		},
	}, c.prepareClick)

	registry.Register(tools.ToolSchema{
		Name:        "browser_set_value",
		Description: "Set the value of an input, textarea, select, or contenteditable element. Uses the native value setter + dispatch input/change events so React/Vue controlled components pick up the change. Activates the tab internally.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab", "element_ref", "value"},
			"properties": map[string]any{
				"tab":         map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"element_ref": map[string]any{"type": "string", "description": "Element ref returned by browser_snapshot."},
				"value":       map[string]any{"type": "string", "description": "Value to set (max 64KB)."},
			},
		},
	}, c.prepareSetValue)

	registry.Register(tools.ToolSchema{
		Name:        "browser_press_key",
		Description: "Send a keyboard key event (keyDown + keyUp) to an agent-owned tab. Common values: Enter, Tab, Escape, ArrowDown, ArrowUp, PageDown, PageUp, Home, End, Backspace, Delete.\n\nCAUTION: On some SPA sites, PageDown/ArrowDown may not scroll because the scroll container is not the window. Use browser_scroll for reliable scrolling.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab", "key"},
			"properties": map[string]any{
				"tab": map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"key": map[string]any{"type": "string", "description": "KeyboardEvent.key value. Single character or named key."},
			},
		},
	}, c.preparePressKey)

	registry.Register(tools.ToolSchema{
		Name:        "browser_wait",
		Description: "Wait N seconds (max 30) for a tab to load or update. Use after navigation, click, or scroll that triggers lazy content loading. Prefer shorter waits (2-4s) and re-snapshot to verify.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab", "seconds"},
			"properties": map[string]any{
				"tab":     map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"seconds": map[string]any{"type": "number", "description": "Seconds to wait (0 < seconds ≤ 30)."},
			},
		},
	}, c.prepareWait)
}

func (c *ExtensionClient) registerEvaluate(registry *tools.Registry) {
	registry.Register(tools.ToolSchema{
		Name:        "browser_evaluate",
		Description: "Execute JavaScript in the page context and return the JSON-serializable result. The script can read/modify the DOM or use the signed-in session.\n\nUse ONLY when dedicated browser tools (snapshot, click, type, scroll) are insufficient. Supports async functions.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab", "script"},
			"properties": map[string]any{
				"tab":    map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"script": map[string]any{"type": "string", "description": "JavaScript expression or async function to evaluate. Return value must be JSON-serializable."},
			},
		},
	}, c.prepareEvaluate)
}

func (c *ExtensionClient) registerInspect(registry *tools.Registry) {
	registry.Register(tools.ToolSchema{
		Name:        "browser_inspect",
		Description: "Return the full HTML source of the page (without selector) or a subtree matching a CSS selector.\n\nWithout selector: returns document.documentElement.outerHTML.\nWith selector: returns outerHTML of the first matching element, or empty string if not found.\n\nUSEFUL FOR: verifying DOM structure when snapshot text isn't enough, inspecting hidden elements, debugging.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab"},
			"properties": map[string]any{
				"tab":      map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"selector": map[string]any{"type": "string", "description": "CSS selector. Returns first match's outerHTML or empty string if no match."},
			},
		},
	}, c.prepareInspect)
}

func (c *ExtensionClient) registerScroll(registry *tools.Registry) {
	registry.Register(tools.ToolSchema{
		Name:        "browser_scroll",
		Description: "Scroll the page in a direction by a pixel amount. Uses a multi-strategy approach to find the actual scrollable container:\n1. document.scrollingElement (standard)\n2. window (traditional pages)\n3. First overflow:auto/scroll DOM element (some SPAs with custom scroll containers)\n4. CDP Input.dispatchMouseEvent mouseWheel (last resort)\n\nReturns {before, after, max} so the caller can verify scrolling happened and whether the page has been scrolled to the bottom.\n\nPREFER THIS over browser_press_key(PageDown) for reliable scrolling, especially on SPAs.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab", "direction"},
			"properties": map[string]any{
				"tab":       map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"direction": map[string]any{"type": "string", "description": "'down' or 'up'."},
				"amount":    map[string]any{"type": "number", "description": "Pixels to scroll. Default 500. Start with 800-1000 for timelines/feeds."},
			},
		},
	}, c.prepareScroll)
}

func (c *ExtensionClient) registerScreenshot(registry *tools.Registry) {
	registry.Register(tools.ToolSchema{
		Name:        "browser_screenshot",
		Description: "Capture a screenshot of a browser tab and save it as a PNG file. Returns the saved file path and byte count.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab"},
			"properties": map[string]any{
				"tab":       map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"selector":  map[string]any{"type": "string", "description": "CSS selector to capture only a specific element. Overrides full_page when set."},
				"full_page": map[string]any{"type": "boolean", "description": "Capture the full scrollable page instead of just the viewport. Ignored when selector is set."},
			},
		},
	}, c.prepareScreenshot)
}

func (c *ExtensionClient) registerNetwork(registry *tools.Registry) {
	registry.Register(tools.ToolSchema{
		Name: "browser_network",
		Description: "Capture and inspect network requests on a tab.\n\n" +
			"Actions:\n" +
			"• 'start' — start capturing. Max 200 entries. Requires tab.\n" +
			"• 'stop' — stop capturing. Requires tab.\n" +
			"• 'list' — list captured requests (URL, method, status, mime type, headers). Requires tab.\n" +
			"• 'detail' — get response body of a specific request. Requires tab + request_id.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab"},
			"properties": map[string]any{
				"tab":        map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"action":     map[string]any{"type": "string", "description": "One of: start, stop, list, detail. Defaults to 'list'."},
				"request_id": map[string]any{"type": "string", "description": "Request ID from a list response. Required for action='detail'."},
			},
		},
	}, c.prepareNetwork)
}

type browserParams interface {
	validate() error
	describe() string
}

func requireBrowserParam(toolName, field, value string) error {
	if value == "" {
		return fmt.Errorf("%s %s must not be empty", toolName, field)
	}
	return nil
}

type tabsParams struct{}

func (*tabsParams) validate() error  { return nil }
func (*tabsParams) describe() string { return "Listing browser tabs" }

type closeTabParams struct {
	Tab string `json:"tab"`
}

func (p *closeTabParams) validate() error {
	return requireBrowserParam("browser_close_tab", "tab", p.Tab)
}
func (*closeTabParams) describe() string { return "Closing browser tab" }

type navigateParams struct {
	Tab    string `json:"tab"`
	URL    string `json:"url"`
	Action string `json:"action"`
}

func (p *navigateParams) validate() error {
	switch p.Action {
	case "new":
		return requireBrowserParam("browser_navigate", "url", p.URL)
	case "back", "forward", "reload":
		return requireBrowserParam("browser_navigate", "tab", p.Tab)
	default:
		if p.Action != "" && p.Action != "go" {
			return fmt.Errorf("browser_navigate unknown action %q", p.Action)
		}
		if err := requireBrowserParam("browser_navigate", "tab", p.Tab); err != nil {
			return err
		}
		return requireBrowserParam("browser_navigate", "url", p.URL)
	}
	return nil
}

func (p *navigateParams) extensionAction() string {
	switch p.Action {
	case "new":
		return "new_tab"
	case "back", "forward", "reload":
		return p.Action
	default:
		return "navigate"
	}
}

func (p *navigateParams) describe() string {
	switch p.Action {
	case "new":
		if p.URL != "" {
			return fmt.Sprintf("Navigating to %s in browser", p.URL)
		}
		return "Creating a blank page"
	case "back":
		return "Navigating backward in browser"
	case "forward":
		return "Navigating forward in browser"
	case "reload":
		return "Reloading in browser"
	default:
		return fmt.Sprintf("Navigating to %s in browser", p.URL)
	}
}

type snapshotParams struct {
	Tab string `json:"tab"`
}

func (p *snapshotParams) validate() error {
	return requireBrowserParam("browser_snapshot", "tab", p.Tab)
}
func (*snapshotParams) describe() string { return "Reading snapshot in browser" }

type clickParams struct {
	Tab        string `json:"tab"`
	ElementRef string `json:"element_ref"`
}

func (p *clickParams) validate() error {
	if err := requireBrowserParam("browser_click", "tab", p.Tab); err != nil {
		return err
	}
	return requireBrowserParam("browser_click", "element_ref", p.ElementRef)
}
func (*clickParams) describe() string { return "Clicking in browser" }

type setValueParams struct {
	Tab        string `json:"tab"`
	ElementRef string `json:"element_ref"`
	Value      string `json:"value"`
}

func (p *setValueParams) validate() error {
	if err := requireBrowserParam("browser_set_value", "tab", p.Tab); err != nil {
		return err
	}
	if err := requireBrowserParam("browser_set_value", "element_ref", p.ElementRef); err != nil {
		return err
	}
	return requireBrowserParam("browser_set_value", "value", p.Value)
}
func (*setValueParams) describe() string { return "Filling element in browser" }

type pressKeyParams struct {
	Tab string `json:"tab"`
	Key string `json:"key"`
}

func (p *pressKeyParams) validate() error {
	if err := requireBrowserParam("browser_press_key", "tab", p.Tab); err != nil {
		return err
	}
	return requireBrowserParam("browser_press_key", "key", p.Key)
}
func (*pressKeyParams) describe() string { return "Pressing keys in browser" }

type waitParams struct {
	Tab     string  `json:"tab"`
	Seconds float64 `json:"seconds"`
}

func (p *waitParams) validate() error {
	if err := requireBrowserParam("browser_wait", "tab", p.Tab); err != nil {
		return err
	}
	if p.Seconds <= 0 || p.Seconds > 30 {
		return fmt.Errorf("browser_wait seconds must be greater than 0 and at most 30")
	}
	return nil
}
func (p *waitParams) describe() string {
	return fmt.Sprintf("Waiting %.2g seconds in browser", p.Seconds)
}

type evaluateParams struct {
	Tab    string `json:"tab"`
	Script string `json:"script"`
}

func (p *evaluateParams) validate() error {
	if err := requireBrowserParam("browser_evaluate", "tab", p.Tab); err != nil {
		return err
	}
	return requireBrowserParam("browser_evaluate", "script", p.Script)
}
func (*evaluateParams) describe() string { return "Evaluating JavaScript in browser" }

type inspectParams struct {
	Tab      string `json:"tab"`
	Selector string `json:"selector,omitempty"`
}

func (p *inspectParams) validate() error {
	return requireBrowserParam("browser_inspect", "tab", p.Tab)
}
func (p *inspectParams) describe() string {
	if p.Selector == "" {
		return "Inspecting page HTML"
	}
	return "Inspecting HTML in browser"
}

type scrollParams struct {
	Tab       string  `json:"tab"`
	Direction string  `json:"direction"`
	Amount    float64 `json:"amount"`
}

func (p *scrollParams) validate() error {
	if err := requireBrowserParam("browser_scroll", "tab", p.Tab); err != nil {
		return err
	}
	if err := requireBrowserParam("browser_scroll", "direction", p.Direction); err != nil {
		return err
	}
	if p.Direction != "down" && p.Direction != "up" {
		return fmt.Errorf("browser_scroll direction must be \"down\" or \"up\"")
	}
	if p.Amount <= 0 {
		p.Amount = 500
	}
	return nil
}
func (p *scrollParams) describe() string {
	return fmt.Sprintf("Scrolling %s in browser", p.Direction)
}

type screenshotParams struct {
	Tab      string `json:"tab"`
	Selector string `json:"selector"`
	FullPage bool   `json:"full_page"`
}

func (p *screenshotParams) validate() error {
	return requireBrowserParam("browser_screenshot", "tab", p.Tab)
}
func (p *screenshotParams) describe() string {
	if p.Selector != "" {
		return "Capturing node screenshot in browser"
	}
	if p.FullPage {
		return "Capturing full page screenshot in browser"
	}
	return "Capturing viewport screenshot in browser"
}

type networkParams struct {
	Tab       string `json:"tab"`
	Action    string `json:"action"`
	RequestID string `json:"request_id"`
}

func (p *networkParams) validate() error {
	if err := requireBrowserParam("browser_network", "tab", p.Tab); err != nil {
		return err
	}
	if p.Action == "detail" {
		return requireBrowserParam("browser_network", "request_id", p.RequestID)
	}
	if p.Action != "" && p.Action != "start" && p.Action != "stop" && p.Action != "list" {
		return fmt.Errorf("browser_network unknown action %q", p.Action)
	}
	return nil
}

func (p *networkParams) extensionAction() string {
	if p.Action == "" || p.Action == "list" {
		return "network_list"
	}
	return "network_" + p.Action
}

func (p *networkParams) describe() string {
	switch p.Action {
	case "start":
		return "Starting network capture in browser"
	case "stop":
		return "Stopping network capture in browser"
	case "detail":
		return "Reading network request in browser"
	default:
		return "Listing network requests in browser"
	}
}

func decodeBrowserParams[T browserParams](args []byte, toolName string, params T) error {
	if err := json.Unmarshal(args, params); err != nil {
		return fmt.Errorf("parse %s args: %w", toolName, err)
	}
	return params.validate()
}

func prepareBrowserCall[T browserParams](c *ExtensionClient, args []byte, toolName, action string, params T) (tools.PreparedTool, error) {
	if err := decodeBrowserParams(args, toolName, params); err != nil {
		return tools.PreparedTool{}, err
	}
	return tools.PreparedTool{
		Description: params.describe(),
		Execute: func(ctx context.Context) (tools.ToolResult, error) {
			result, err := c.call(ctx, action, params)
			if err != nil {
				return tools.ErrorResult(fmt.Errorf("%s: %w", toolName, err)), nil
			}
			return tools.TextResult(c.truncate(ctx, result)), nil
		},
	}, nil
}

func (c *ExtensionClient) prepareTabs(args []byte) (tools.PreparedTool, error) {
	return prepareBrowserCall(c, args, "browser_tabs", "tabs", &tabsParams{})
}

func (c *ExtensionClient) prepareCloseTab(args []byte) (tools.PreparedTool, error) {
	return prepareBrowserCall(c, args, "browser_close_tab", "close_tab", &closeTabParams{})
}

func (c *ExtensionClient) prepareNavigate(args []byte) (tools.PreparedTool, error) {
	params := &navigateParams{}
	if err := decodeBrowserParams(args, "browser_navigate", params); err != nil {
		return tools.PreparedTool{}, err
	}
	action := params.extensionAction()
	return tools.PreparedTool{
		Description: params.describe(),
		Execute: func(ctx context.Context) (tools.ToolResult, error) {
			result, err := c.call(ctx, action, params)
			if err != nil {
				return tools.ErrorResult(fmt.Errorf("browser_navigate: %w", err)), nil
			}
			return tools.TextResult(c.truncate(ctx, result)), nil
		},
	}, nil
}

func (c *ExtensionClient) prepareSnapshot(args []byte) (tools.PreparedTool, error) {
	return prepareBrowserCall(c, args, "browser_snapshot", "snapshot", &snapshotParams{})
}

func (c *ExtensionClient) prepareClick(args []byte) (tools.PreparedTool, error) {
	return prepareBrowserCall(c, args, "browser_click", "click", &clickParams{})
}

func (c *ExtensionClient) prepareSetValue(args []byte) (tools.PreparedTool, error) {
	return prepareBrowserCall(c, args, "browser_set_value", "set_value", &setValueParams{})
}

func (c *ExtensionClient) preparePressKey(args []byte) (tools.PreparedTool, error) {
	return prepareBrowserCall(c, args, "browser_press_key", "press_key", &pressKeyParams{})
}

func (c *ExtensionClient) prepareWait(args []byte) (tools.PreparedTool, error) {
	return prepareBrowserCall(c, args, "browser_wait", "wait", &waitParams{})
}

func (c *ExtensionClient) prepareEvaluate(args []byte) (tools.PreparedTool, error) {
	return prepareBrowserCall(c, args, "browser_evaluate", "evaluate", &evaluateParams{})
}

func (c *ExtensionClient) prepareInspect(args []byte) (tools.PreparedTool, error) {
	return prepareBrowserCall(c, args, "browser_inspect", "inspect", &inspectParams{})
}

func (c *ExtensionClient) prepareScroll(args []byte) (tools.PreparedTool, error) {
	return prepareBrowserCall(c, args, "browser_scroll", "scroll", &scrollParams{Amount: 500})
}

func (c *ExtensionClient) prepareScreenshot(args []byte) (tools.PreparedTool, error) {
	params := &screenshotParams{}
	if err := decodeBrowserParams(args, "browser_screenshot", params); err != nil {
		return tools.PreparedTool{}, err
	}
	return tools.PreparedTool{
		Description: params.describe(),
		Execute: func(ctx context.Context) (tools.ToolResult, error) {
			result, err := c.call(ctx, "screenshot", params)
			if err != nil {
				return tools.ErrorResult(fmt.Errorf("browser_screenshot: %w", err)), nil
			}
			if result == "" {
				return tools.ErrorResult(fmt.Errorf("browser_screenshot: empty screenshot data")), nil
			}
			data, err := base64.StdEncoding.DecodeString(result)
			if err != nil {
				return tools.ErrorResult(fmt.Errorf("browser_screenshot: decode base64: %w", err)), nil
			}
			path, err := c.rt.WriteTmpFile(ctx, string(data))
			if err != nil {
				return tools.ErrorResult(fmt.Errorf("browser_screenshot: write tmp file: %w", err)), nil
			}
			return tools.TextResult(fmt.Sprintf(`{"path":%q,"bytes":%d}`, path, len(data))), nil
		},
	}, nil
}

func (c *ExtensionClient) prepareNetwork(args []byte) (tools.PreparedTool, error) {
	params := &networkParams{}
	if err := decodeBrowserParams(args, "browser_network", params); err != nil {
		return tools.PreparedTool{}, err
	}
	action := params.extensionAction()
	return tools.PreparedTool{
		Description: params.describe(),
		Execute: func(ctx context.Context) (tools.ToolResult, error) {
			result, err := c.call(ctx, action, params)
			if err != nil {
				return tools.ErrorResult(fmt.Errorf("browser_network: %w", err)), nil
			}
			return tools.TextResult(c.truncate(ctx, result)), nil
		},
	}, nil
}
