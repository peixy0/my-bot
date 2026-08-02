import { ensureAttached, cdpSend, type CdpEvaluateResult } from "./cdp.js";
import { keyCodes, maxTextLength } from "./constants.js";
import type { ClickInfo, ClickError, SetValueResult } from "./types.js";

export async function cdpClick(tabId: number, elementRef: string): Promise<{ clicked: boolean }> {
  await ensureAttached(tabId);
  await chrome.tabs.update(tabId, { active: true }).catch(() => {});

  const data = (await cdpSend(tabId, "Runtime.evaluate", {
    expression: `(() => {
      const el = window.__agentSnapshotRefs?.[${JSON.stringify(elementRef)}];
      if (!el) return JSON.stringify({ error: "stale" });
      if (!document.body.contains(el)) return JSON.stringify({ error: "stale" });
      el.scrollIntoView({ block: "center", behavior: "instant" });
      const rect = el.getBoundingClientRect();
      const style = window.getComputedStyle(el);
      const visible = style.display !== "none" && style.visibility !== "hidden" && parseFloat(style.opacity) !== 0;
      const pointerEventsNone = style.pointerEvents === "none";
      const htmlEl = el instanceof HTMLElement ? el : null;
      const disabled = htmlEl ? htmlEl.disabled : false;
      const ariaDisabled = el.getAttribute("aria-disabled") === "true";
      return JSON.stringify({
        x: rect.left + rect.width / 2,
        y: rect.top + rect.height / 2,
        w: rect.width,
        h: rect.height,
        isHtml: !!htmlEl,
        visible,
        pointerEventsNone,
        disabled: disabled || ariaDisabled,
      });
    })()`,
    returnByValue: true,
    awaitPromise: true,
  })) as CdpEvaluateResult;

  const probe: ClickInfo | ClickError = JSON.parse(String(data.result?.value ?? "{}"));
  if ("error" in probe) {
    throw new Error("element_ref is stale; call browser_snapshot again");
  }

  if (probe.disabled) {
    throw new Error("element is disabled");
  }
  if (probe.pointerEventsNone) {
    throw new Error("element has pointer-events: none");
  }

  const { x, y, isHtml, visible } = probe;

  if (isHtml && visible !== false) {
    const clickResult = (await cdpSend(tabId, "Runtime.evaluate", {
      expression: `(() => {
        const el = window.__agentSnapshotRefs?.[${JSON.stringify(elementRef)}];
        if (!el || !document.body.contains(el)) return JSON.stringify({ ok: false, reason: "stale" });
        try {
          (el).click();
          return JSON.stringify({ ok: true });
        } catch (e) {
          return JSON.stringify({ ok: false, reason: String(e) });
        }
      })()`,
      returnByValue: true,
      awaitPromise: true,
    })) as CdpEvaluateResult;

    const clickInfo = JSON.parse(String(clickResult.result?.value ?? "{}"));
    if (clickInfo.ok) {
      return { clicked: true };
    }
    if (clickInfo.reason === "stale") {
      throw new Error("element_ref is stale; call browser_snapshot again");
    }
  }

  await cdpSend(tabId, "Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
  await cdpSend(tabId, "Input.dispatchMouseEvent", {
    type: "mousePressed",
    x,
    y,
    button: "left",
    clickCount: 1,
  });
  await cdpSend(tabId, "Input.dispatchMouseEvent", {
    type: "mouseReleased",
    x,
    y,
    button: "left",
    clickCount: 1,
  });
  return { clicked: true };
}

export async function cdpSetValue(tabId: number, elementRef: string, value: string): Promise<{ set: boolean }> {
  if (typeof value !== "string" || value.length > maxTextLength) {
    throw new Error("value is too long");
  }
  await ensureAttached(tabId);
  await chrome.tabs.update(tabId, { active: true }).catch(() => {});

  const data = (await cdpSend(tabId, "Runtime.evaluate", {
    expression: `(() => {
      const el = window.__agentSnapshotRefs?.[${JSON.stringify(elementRef)}];
      if (!el || !document.body.contains(el)) return JSON.stringify({ error: "stale" });
      el.scrollIntoView({ block: "center", behavior: "instant" });
      el.focus();
      const tag = el.tagName;
      const val = ${JSON.stringify(value)};
      if (tag === "INPUT" || tag === "TEXTAREA") {
        const proto = tag === "INPUT" ? HTMLInputElement.prototype : HTMLTextAreaElement.prototype;
        const setter = Object.getOwnPropertyDescriptor(proto, "value")?.set;
        if (setter) { setter.call(el, val); } else { el.value = val; }
        el.dispatchEvent(new Event("input", { bubbles: true }));
        el.dispatchEvent(new Event("change", { bubbles: true }));
        return JSON.stringify({ set: true, tag });
      }
      if (tag === "SELECT") {
        el.value = val;
        el.dispatchEvent(new Event("input", { bubbles: true }));
        el.dispatchEvent(new Event("change", { bubbles: true }));
        return JSON.stringify({ set: true, tag });
      }
      if (el.isContentEditable) {
        el.innerHTML = val;
        el.dispatchEvent(new Event("input", { bubbles: true }));
        return JSON.stringify({ set: true, tag: "contenteditable" });
      }
      return JSON.stringify({ error: "element is not settable", tag });
    })()`,
    returnByValue: true,
    awaitPromise: true,
  })) as CdpEvaluateResult;

  const info: SetValueResult = JSON.parse(String(data.result?.value ?? "{}"));
  if (info.error === "stale") {
    throw new Error("element_ref is stale; call browser_snapshot again");
  }
  if (info.error) {
    throw new Error(info.error);
  }
  return { set: true };
}

export async function cdpPressKey(tabId: number, key: string): Promise<{ key: string }> {
  if (typeof key !== "string" || key.length > 64) {
    throw new Error("key is invalid");
  }
  await ensureAttached(tabId);
  await chrome.tabs.update(tabId, { active: true }).catch(() => {});

  const keyCode = keyCodes[key] ?? key.charCodeAt(0);
  const code = key.length === 1 ? "Key" + key.toUpperCase() : key;
  const vk = keyToWindowsKeyCode(key, keyCode);

  await cdpSend(tabId, "Runtime.evaluate", {
    expression: "document.activeElement?.focus()",
    returnByValue: true,
  });

  await cdpSend(tabId, "Input.dispatchKeyEvent", {
    type: "keyDown",
    key,
    code,
    windowsVirtualKeyCode: vk,
    nativeVirtualKeyCode: vk,
  });
  await cdpSend(tabId, "Input.dispatchKeyEvent", {
    type: "char",
    key,
    code,
    windowsVirtualKeyCode: vk,
    nativeVirtualKeyCode: vk,
  });
  await cdpSend(tabId, "Input.dispatchKeyEvent", {
    type: "keyUp",
    key,
    code,
    windowsVirtualKeyCode: vk,
    nativeVirtualKeyCode: vk,
  });
  return { key };
}

export async function cdpScroll(
  tabId: number,
  direction: string,
  amount?: number,
): Promise<{ before: number; after: number; max: number }> {
  const delta = direction === "up" ? -Math.abs(amount || 500) : Math.abs(amount || 500);
  await ensureAttached(tabId);

  const data = (await cdpSend(tabId, "Runtime.evaluate", {
    expression: `(function() {
      const delta = ${JSON.stringify(delta)};

      const se = document.scrollingElement;
      if (se && se.scrollHeight > se.clientHeight) {
        const before = se.scrollTop;
        se.scrollBy(0, delta);
        return JSON.stringify({before,after:se.scrollTop,max:se.scrollHeight-se.clientHeight});
      }

      const de = document.documentElement;
      if (de.scrollHeight > window.innerHeight) {
        const before = window.scrollY;
        window.scrollBy(0, delta);
        return JSON.stringify({before,after:window.scrollY,max:de.scrollHeight-window.innerHeight});
      }

      const all = document.querySelectorAll("*");
      for (const el of all) {
        if (el.scrollHeight > el.clientHeight) {
          const style = getComputedStyle(el);
          if (style.overflowY === "auto" || style.overflowY === "scroll" || style.overflow === "auto" || style.overflow === "scroll") {
            const before = el.scrollTop;
            el.scrollBy(0, delta);
            return JSON.stringify({before,after:el.scrollTop,max:el.scrollHeight-el.clientHeight});
          }
        }
      }
      return JSON.stringify({before:0,after:0,max:0});
    })()`,
    returnByValue: true,
    awaitPromise: true,
  })) as CdpEvaluateResult;

  const jsInfo = JSON.parse(String(data.result?.value ?? "{}"));

  if (jsInfo.before === 0 && jsInfo.after === 0) {
    await cdpSend(tabId, "Input.dispatchMouseEvent", {
      type: "mouseWheel",
      x: 100,
      y: 100,
      deltaX: 0,
      deltaY: delta,
    });
  }

  return jsInfo;
}

export async function evaluate(tabId: number, script: string): Promise<unknown> {
  if (typeof script !== "string" || !script.trim()) {
    throw new Error("script is required");
  }
  await ensureAttached(tabId);
  const data = (await cdpSend(tabId, "Runtime.evaluate", {
    expression: script,
    awaitPromise: true,
    returnByValue: true,
  })) as CdpEvaluateResult;

  if (data.exceptionDetails) {
    throw new Error(formatException(data.exceptionDetails));
  }
  return data.result?.value ?? null;
}

export async function cdpGetHTML(tabId: number, selector?: string): Promise<string> {
  await ensureAttached(tabId);
  const expression = selector
    ? `(() => { const el = document.querySelector(${JSON.stringify(selector)}); return el ? el.outerHTML : ''; })()`
    : "document.documentElement.outerHTML";
  const data = (await cdpSend(tabId, "Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true,
  })) as CdpEvaluateResult;

  if (data.exceptionDetails) {
    throw new Error(formatException(data.exceptionDetails));
  }
  return String(data.result?.value ?? "");
}

const windowsVirtualKeyCodes: Record<string, number> = {
  Enter: 13,
  Tab: 9,
  Escape: 27,
  Backspace: 8,
  Delete: 46,
  Insert: 45,
  Space: 32,
  ArrowDown: 40,
  ArrowUp: 38,
  ArrowLeft: 37,
  ArrowRight: 39,
  PageDown: 34,
  PageUp: 33,
  Home: 36,
  End: 35,
  Control: 17,
  Shift: 16,
  Alt: 18,
  Meta: 91,
  F1: 112,
  F2: 113,
  F3: 114,
  F4: 115,
  F5: 116,
  F6: 117,
  F7: 118,
  F8: 119,
  F9: 120,
  F10: 121,
  F11: 122,
  F12: 123,
};

function keyToWindowsKeyCode(key: string, fallback: number): number {
  if (key.length === 1) {
    const upper = key.toUpperCase();
    if (upper >= "A" && upper <= "Z") return upper.charCodeAt(0);
    if (key >= "0" && key <= "9") return key.charCodeAt(0);
  }
  return windowsVirtualKeyCodes[key] ?? fallback;
}

function formatException(details: NonNullable<CdpEvaluateResult["exceptionDetails"]>): string {
  const text = details.exception?.description || details.text || "Unknown error";
  const url = details.url ? ` (${details.url}:${details.lineNumber}:${details.columnNumber})` : "";
  return `${text}${url}`;
}
