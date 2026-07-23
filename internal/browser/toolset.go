package browser

import (
	"context"
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

func (c *ExtensionClient) call(ctx context.Context, action string, params any) (json.RawMessage, error) {
	return c.caller.Call(ctx, c.scopeID, action, params)
}

func (c *ExtensionClient) truncate(ctx context.Context, text string) string {
	return c.rt.Truncate(ctx, text, c.maxChars)
}

func (c *ExtensionClient) Register(registry *tools.Registry) {
	registry.Register(tools.ToolSchema{
		Name:          "browser_tabs",
		Description:   "List only the browser tabs owned by this agent. Each returned tab may be used for later browser actions. Tab refs persist until the tab is closed.",
		ParameterDesc: map[string]any{"type": "object", "properties": map[string]any{}},
	}, c.handleTabs)

	registry.Register(tools.ToolSchema{
		Name:        "browser_new_tab",
		Description: "Create a new browser tab owned by this agent and optionally navigate it. Prefer this over creating a tab then navigating separately — SPAs may intercept navigation after page load, so passing the final URL at creation time is more reliable.",
		ParameterDesc: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "description": "Absolute URL to open. Defaults to about:blank. Pass the final destination URL here to avoid SPA navigation intercept."},
			},
		},
	}, c.handleNewTab)

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
	}, c.handleCloseTab)

	registry.Register(tools.ToolSchema{
		Name:        "browser_activate_tab",
		Description: "Activate (bring to foreground) an agent-owned browser tab. Useful before typing into inputs since browsers suppress keyboard events on background tabs.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab"},
			"properties": map[string]any{
				"tab": map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
			},
		},
	}, c.handleActivateTab)

	registry.Register(tools.ToolSchema{
		Name:        "browser_navigate",
		Description: "Navigate a tab to a URL. After navigation call browser_snapshot to confirm the actual URL — SPAs may intercept or redirect, so the final URL can differ from the requested one.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab", "url"},
			"properties": map[string]any{
				"tab": map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"url": map[string]any{"type": "string", "description": "Absolute URL to navigate to."},
			},
		},
	}, c.handleNavigate)

	registry.Register(tools.ToolSchema{
		Name:        "browser_snapshot",
		Description: "Read visible page text and interactive elements from a tab. Use returned element refs for browser_click, browser_type, and browser_select_option.\n\nIMPORTANT:\n• Element refs are RESET on every snapshot — always snapshot fresh before interacting.\n• SPAs may not expose all content until scrolled. Call browser_scroll first to trigger lazy loading.\n• Hidden elements (zero size, display:none, visibility:hidden, type=hidden) are excluded.\n• At most 300 interactive elements are returned per snapshot.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab"},
			"properties": map[string]any{
				"tab": map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
			},
		},
	}, c.handleSnapshot)

	registry.Register(tools.ToolSchema{
		Name:        "browser_click",
		Description: "Click an element ref from the latest browser_snapshot of an agent-owned tab. Scrolls the element into view first, then clicks at its center via CDP Input.dispatchMouseEvent.\n\nNOTE: Element refs become STALE after a new browser_snapshot. Always snapshot then click within the same interaction sequence.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab", "element_ref"},
			"properties": map[string]any{
				"tab":         map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"element_ref": map[string]any{"type": "string", "description": "Element ref returned by browser_snapshot (1-based integer as string, e.g. '42'). Stale refs produce 'element_ref is stale' error — re-snapshot to get fresh refs."},
			},
		},
	}, c.handleClick)

	registry.Register(tools.ToolSchema{
		Name:        "browser_type",
		Description: "Clear and replace the value of an input/textarea element. Uses Ctrl+A → Backspace → insert text character by character via CDP Input.dispatchKeyEvent. Activates the tab first since browsers suppress keyboard events on background tabs.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab", "element_ref", "text"},
			"properties": map[string]any{
				"tab":         map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"element_ref": map[string]any{"type": "string", "description": "Element ref returned by browser_snapshot."},
				"text":        map[string]any{"type": "string", "description": "Text to enter (max 64KB)."},
			},
		},
	}, c.handleType)

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
	}, c.handlePressKey)

	registry.Register(tools.ToolSchema{
		Name:        "browser_select_option",
		Description: "Select an option in a select element by value. Uses Runtime.callFunctionOn to set value and dispatch input/change events.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab", "element_ref", "value"},
			"properties": map[string]any{
				"tab":         map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"element_ref": map[string]any{"type": "string", "description": "Element ref returned by browser_snapshot."},
				"value":       map[string]any{"type": "string", "description": "Option value (the 'value' attribute, not display text)."},
			},
		},
	}, c.handleSelectOption)

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
	}, c.handleWait)

	registry.Register(tools.ToolSchema{
		Name:        "browser_evaluate",
		Description: "Execute JavaScript in the page context and return the JSON-serializable result. The script can read/modify the DOM or use the signed-in session.\n\nUse ONLY when dedicated browser tools (snapshot, click, type, scroll) are insufficient. Supports async functions. Exceptions include line:column information.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab", "script"},
			"properties": map[string]any{
				"tab":    map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"script": map[string]any{"type": "string", "description": "JavaScript expression or async function to evaluate. Return value must be JSON-serializable."},
			},
		},
	}, c.handleEvaluate)

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
	}, c.handleInspect)

	registry.Register(tools.ToolSchema{
		Name:        "browser_scroll",
		Description: "Scroll the page in a direction by a pixel amount. Uses a multi-strategy approach to find the actual scrollable container:\n1. document.scrollingElement (standard)\n2. window (traditional pages)\n3. First overflow:auto/scroll DOM element (some SPAs with custom scroll containers)\n4. CDP Input.dispatchMouseEvent mouseWheel (last resort)\n\nReturns {method, before, after} so the caller can verify scrolling happened.\n\nPREFER THIS over browser_press_key(PageDown) for reliable scrolling, especially on SPAs.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab", "direction"},
			"properties": map[string]any{
				"tab":       map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
				"direction": map[string]any{"type": "string", "description": "'down' or 'up'."},
				"amount":    map[string]any{"type": "number", "description": "Pixels to scroll. Default 500. Start with 800-1000 for timelines/feeds."},
			},
		},
	}, c.handleScroll)

	registry.Register(tools.ToolSchema{
		Name:        "browser_back",
		Description: "Navigate back one page in the tab's browser history stack. Uses CDP Page.getNavigationHistory + Page.navigateToHistoryEntry.\n\nNOTE: SPA internal navigation may NOT create browser history entries. This only works for actual page-level navigations.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab"},
			"properties": map[string]any{
				"tab": map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
			},
		},
	}, c.handleBack)

	registry.Register(tools.ToolSchema{
		Name:        "browser_forward",
		Description: "Navigate forward one page in the tab's browser history stack. Uses CDP Page.getNavigationHistory + Page.navigateToHistoryEntry.\n\nNOTE: Only works for actual page-level navigations, not SPA internal routing.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab"},
			"properties": map[string]any{
				"tab": map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
			},
		},
	}, c.handleForward)

	registry.Register(tools.ToolSchema{
		Name:        "browser_reload",
		Description: "Reload the current page in the tab via CDP Page.reload(). Useful to reset SPA state or refresh dynamic content.",
		ParameterDesc: map[string]any{
			"type":     "object",
			"required": []string{"tab"},
			"properties": map[string]any{
				"tab": map[string]any{"type": "string", "description": "A tab returned by browser_tabs."},
			},
		},
	}, c.handleReload)
}

func (c *ExtensionClient) handleTabs(ctx context.Context, args []byte) (tools.ToolResult, error) {
	result, err := c.call(ctx, "tabs", struct{}{})
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_tabs: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleNewTab(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_new_tab: parse args: %w", err)
	}
	result, err := c.call(ctx, "new_tab", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_new_tab: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleCloseTab(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab string `json:"tab"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_close_tab: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_close_tab: tab is required")), nil
	}
	result, err := c.call(ctx, "close_tab", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_close_tab: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleActivateTab(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab string `json:"tab"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_activate_tab: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_activate_tab: tab is required")), nil
	}
	result, err := c.call(ctx, "activate_tab", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_activate_tab: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleNavigate(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab string `json:"tab"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_navigate: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_navigate: tab is required")), nil
	}
	if params.URL == "" {
		return tools.ErrorResult(fmt.Errorf("browser_navigate: url is required")), nil
	}
	result, err := c.call(ctx, "navigate", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_navigate: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleSnapshot(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab string `json:"tab"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_snapshot: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_snapshot: tab is required")), nil
	}
	result, err := c.call(ctx, "snapshot", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_snapshot: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleClick(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab        string `json:"tab"`
		ElementRef string `json:"element_ref"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_click: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_click: tab is required")), nil
	}
	if params.ElementRef == "" {
		return tools.ErrorResult(fmt.Errorf("browser_click: element_ref is required")), nil
	}
	result, err := c.call(ctx, "click", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_click: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleType(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab        string `json:"tab"`
		ElementRef string `json:"element_ref"`
		Text       string `json:"text"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_type: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_type: tab is required")), nil
	}
	if params.ElementRef == "" {
		return tools.ErrorResult(fmt.Errorf("browser_type: element_ref is required")), nil
	}
	if params.Text == "" {
		return tools.ErrorResult(fmt.Errorf("browser_type: text is required")), nil
	}
	result, err := c.call(ctx, "type", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_type: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handlePressKey(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab string `json:"tab"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_press_key: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_press_key: tab is required")), nil
	}
	if params.Key == "" {
		return tools.ErrorResult(fmt.Errorf("browser_press_key: key is required")), nil
	}
	result, err := c.call(ctx, "press_key", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_press_key: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleSelectOption(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab        string `json:"tab"`
		ElementRef string `json:"element_ref"`
		Value      string `json:"value"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_select_option: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_select_option: tab is required")), nil
	}
	if params.ElementRef == "" {
		return tools.ErrorResult(fmt.Errorf("browser_select_option: element_ref is required")), nil
	}
	if params.Value == "" {
		return tools.ErrorResult(fmt.Errorf("browser_select_option: value is required")), nil
	}
	result, err := c.call(ctx, "select_option", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_select_option: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleWait(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab     string  `json:"tab"`
		Seconds float64 `json:"seconds"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_wait: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_wait: tab is required")), nil
	}
	if params.Seconds <= 0 || params.Seconds > 30 {
		return tools.ErrorResult(fmt.Errorf("browser_wait: seconds must be between 0 and 30")), nil
	}
	result, err := c.call(ctx, "wait", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_wait: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleEvaluate(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab    string `json:"tab"`
		Script string `json:"script"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_evaluate: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_evaluate: tab is required")), nil
	}
	if strings.TrimSpace(params.Script) == "" {
		return tools.ErrorResult(fmt.Errorf("browser_evaluate: script is required")), nil
	}
	result, err := c.call(ctx, "evaluate", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_evaluate: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleInspect(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab      string `json:"tab"`
		Selector string `json:"selector,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_inspect: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_inspect: tab is required")), nil
	}
	result, err := c.call(ctx, "inspect", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_inspect: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleScroll(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab       string  `json:"tab"`
		Direction string  `json:"direction"`
		Amount    float64 `json:"amount"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_scroll: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_scroll: tab is required")), nil
	}
	if params.Direction != "down" && params.Direction != "up" {
		return tools.ErrorResult(fmt.Errorf("browser_scroll: direction must be 'down' or 'up'")), nil
	}
	if params.Amount <= 0 {
		params.Amount = 500
	}
	result, err := c.call(ctx, "scroll", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_scroll: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleBack(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab string `json:"tab"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_back: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_back: tab is required")), nil
	}
	result, err := c.call(ctx, "back", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_back: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleForward(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab string `json:"tab"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_forward: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_forward: tab is required")), nil
	}
	result, err := c.call(ctx, "forward", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_forward: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}

func (c *ExtensionClient) handleReload(ctx context.Context, args []byte) (tools.ToolResult, error) {
	var params struct {
		Tab string `json:"tab"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return tools.ToolResult{}, fmt.Errorf("browser_reload: parse args: %w", err)
	}
	if params.Tab == "" {
		return tools.ErrorResult(fmt.Errorf("browser_reload: tab is required")), nil
	}
	result, err := c.call(ctx, "reload", params)
	if err != nil {
		return tools.ErrorResult(fmt.Errorf("browser_reload: %w", err)), nil
	}
	return tools.TextResult(c.truncate(ctx, string(result))), nil
}
