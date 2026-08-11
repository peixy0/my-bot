import {
  getConnectionStatus,
  setConnectionEnabled,
  restartConnection,
  disconnect,
  send,
  getSocket,
} from "./connection.js";
import {
  listTabs,
  newTab,
  closeTab,
  navigate,
  withTab,
  closeScope,
  removeTab,
  setActiveTab,
  adoptOpenedTab,
} from "./tabs.js";
import { cdpSnapshot } from "./snapshot.js";
import { cdpClick, cdpSetValue, cdpPressKey, cdpScroll, evaluate, cdpGetHTML } from "./actions.js";
import { networkStart, networkStop, networkList, networkDetail } from "./network.js";
import { ensureAttached, detachTab, cdpSend, waitForLoad, type CdpEvaluateResult } from "./cdp.js";
import { isRequestFrame } from "./types.js";
import type { Connection, NetworkBuffer, RequestFrame } from "./types.js";

const networkBuffers = new Map<number, NetworkBuffer>();
const screenshotChunkSize = 256 * 1024;

chrome.tabs.onCreated.addListener((tab) => {
  adoptOpenedTab(tab);
});

chrome.tabs.onRemoved.addListener((tabId) => {
  removeTab(tabId, networkBuffers);
});

chrome.tabs.onActivated.addListener((activeInfo) => {
  setActiveTab(activeInfo.tabId);
});

chrome.debugger.onDetach.addListener((source) => {
  if (source.tabId) void detachTab(source.tabId);
});

chrome.runtime.onMessage.addListener((message) => {
  const msg = message as Record<string, unknown>;
  if (msg?.type === "browserBridgeStatus") {
    return Promise.resolve(getConnectionStatus());
  }
  if (msg?.type === "browserBridgeReconnect") {
    setConnectionEnabled(true);
    restartConnection("manual connection", handleFrame);
    return Promise.resolve(getConnectionStatus());
  }
  if (msg?.type === "browserBridgeDisconnect") {
    setConnectionEnabled(false);
    disconnect("Disconnected by user.");
    return Promise.resolve(getConnectionStatus());
  }
  if (msg?.type === "browserBridgeSettingsChanged") {
    setConnectionEnabled(false);
    disconnect("Settings changed. Connect again to use the new endpoint.");
    return Promise.resolve(getConnectionStatus());
  }
  return undefined;
});

async function handleFrame(raw: string, connection: Connection): Promise<void> {
  if (connection !== getSocket() || typeof raw !== "string" || raw.length > 1024 * 1024) return;

  let frame: RequestFrame;
  try {
    frame = JSON.parse(raw);
  } catch {
    return;
  }
  if (!isRequestFrame(frame)) return;

  try {
    await execute(frame.scope_id, frame.action, frame.params || {}, frame.id);
  } catch (error: unknown) {
    send({
      type: "response",
      id: frame.id,
      error: error instanceof Error ? error.message : String(error),
      has_more: false,
    });
  }
}

async function execute(
  scopeId: string,
  action: string,
  params: Record<string, unknown>,
  requestId: string,
): Promise<void> {
  if (!isPlainObject(params)) {
    throw new Error("request params must be an object");
  }

  switch (action) {
    case "tabs":
      return sendResult(requestId, await listTabs(scopeId));
    case "new_tab":
      return sendResult(requestId, await newTab(scopeId, params.url as string));
    case "close_tab":
      return sendResult(requestId, await closeTab(scopeId, params.tab as string));
    case "navigate":
      return sendResult(requestId, await navigate(scopeId, params.tab as string, params.url as string));
    case "snapshot":
      return sendResult(requestId, await withTab(scopeId, params.tab as string, (tab) => cdpSnapshot(tab.id!)));
    case "click":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, (tab) => cdpClick(tab.id!, params.element_ref as string)),
      );
    case "set_value":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, (tab) =>
          cdpSetValue(tab.id!, params.element_ref as string, params.value as string),
        ),
      );
    case "press_key":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, (tab) => cdpPressKey(tab.id!, params.key as string)),
      );
    case "wait":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, (tab) => cdpWait(tab.id!, params.seconds as number)),
      );
    case "evaluate":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, (tab) => evaluate(tab.id!, params.script as string)),
      );
    case "inspect":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, (tab) =>
          cdpGetHTML(tab.id!, params.selector as string | undefined),
        ),
      );
    case "scroll":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, (tab) =>
          cdpScroll(tab.id!, params.direction as string, params.amount as number | undefined),
        ),
      );
    case "back":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, (tab) => cdpNavigateHistory(tab.id!, "back")),
      );
    case "forward":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, (tab) => cdpNavigateHistory(tab.id!, "forward")),
      );
    case "reload":
      return sendResult(requestId, await withTab(scopeId, params.tab as string, (tab) => cdpReload(tab.id!)));
    case "screenshot":
      return withTab(scopeId, params.tab as string, (tab) => cdpScreenshot(tab.id!, params, requestId));
    case "network_start":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, (tab) => networkStart(tab.id!, networkBuffers)),
      );
    case "network_stop":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, (tab) => networkStop(tab.id!, networkBuffers)),
      );
    case "network_list":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, async (tab) => networkList(tab.id!, networkBuffers)),
      );
    case "network_detail":
      return sendResult(
        requestId,
        await withTab(scopeId, params.tab as string, (tab) =>
          networkDetail(tab.id!, params.request_id as string, networkBuffers),
        ),
      );
    case "scope_close":
      return sendResult(requestId, await closeScope(scopeId, networkBuffers));
    default:
      throw new Error(`unknown browser action: ${action}`);
  }
}

async function cdpWait(tabId: number, seconds?: number): Promise<{ waited: boolean }> {
  const sec = typeof seconds === "number" && seconds > 0 && seconds <= 30 ? seconds : 2;
  await ensureAttached(tabId);
  const data = (await cdpSend(tabId, "Runtime.evaluate", {
    expression: "document.readyState",
    returnByValue: true,
  })) as CdpEvaluateResult;
  if (data.result?.value !== "complete") {
    await waitForLoad(tabId, sec * 1000);
  } else {
    await new Promise((r) => setTimeout(r, sec * 1000));
  }
  return { waited: true };
}

async function cdpNavigateHistory(tabId: number, direction: "back" | "forward"): Promise<{ navigated: boolean }> {
  await ensureAttached(tabId);
  const data = (await cdpSend(tabId, "Runtime.evaluate", {
    expression: `(() => { history.${direction}(); return true; })()`,
    returnByValue: true,
    awaitPromise: true,
  })) as CdpEvaluateResult;
  return { navigated: Boolean(data.result?.value) };
}

async function cdpReload(tabId: number): Promise<{ reloaded: boolean }> {
  await ensureAttached(tabId);
  await cdpSend(tabId, "Page.reload", {});
  return { reloaded: true };
}

async function cdpScreenshot(tabId: number, params: Record<string, unknown>, requestId: string): Promise<void> {
  await ensureAttached(tabId);
  const format = (params.format as string) === "png" ? "png" : "jpeg";
  const quality = format === "jpeg" ? Math.min(100, Math.max(0, Number(params.quality) || 80)) : undefined;
  const captureParams: Record<string, unknown> = { format };
  if (quality !== undefined) captureParams.quality = quality;
  if (params.full_page) captureParams.captureBeyondViewport = true;

  const result = await cdpSend(tabId, "Page.captureScreenshot", captureParams);
  if (!result.data) {
    throw new Error("screenshot failed");
  }
  const screenshot = String(result.data);
  for (let start = 0; start < screenshot.length; start += screenshotChunkSize) {
    const end = start + screenshotChunkSize;
    send({
      type: "response",
      id: requestId,
      data: screenshot.slice(start, end),
      has_more: end < screenshot.length,
    });
  }
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function sendResult(requestId: string, result: unknown): void {
  send({ type: "response", id: requestId, data: JSON.stringify(result, null, 2), has_more: false });
}

console.info("[My Bot] Service worker loaded.");

setConnectionEnabled(true);
restartConnection("service worker restart", handleFrame);
