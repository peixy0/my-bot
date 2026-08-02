import type { NetworkBuffer } from "./types.js";

const attachedTabs = new Set<number>();

export function isAttached(tabId: number): boolean {
  return attachedTabs.has(tabId);
}

export async function ensureAttached(tabId: number): Promise<void> {
  if (attachedTabs.has(tabId)) return;
  try {
    await chrome.debugger.attach({ tabId }, "1.3");
    attachedTabs.add(tabId);
  } catch (error: unknown) {
    if (error instanceof Error && error.message.includes("Another debugger is already attached")) {
      throw new Error("DevTools is open on this tab; close it and retry");
    }
    throw error;
  }
}

export async function detachTab(tabId: number): Promise<void> {
  if (!attachedTabs.has(tabId)) return;
  attachedTabs.delete(tabId);
  await chrome.debugger.detach({ tabId }).catch(() => {});
}

export function cleanupNetworkBuffer(tabId: number, networkBuffers: Map<number, NetworkBuffer>): void {
  const buffer = networkBuffers.get(tabId);
  if (!buffer) return;
  if (buffer.listener) {
    chrome.debugger.onEvent.removeListener(buffer.listener);
  }
  networkBuffers.delete(tabId);
}

export interface CdpBase {
  [key: string]: unknown;
}

export interface CdpEvaluateResult extends CdpBase {
  result?: { value?: unknown; objectId?: string };
  exceptionDetails?: {
    exception?: { description?: string };
    text?: string;
    url?: string;
    lineNumber?: number;
    columnNumber?: number;
  };
}

export interface CdpAxTreeResult extends CdpBase {
  nodes?: Array<{
    nodeId: string;
    ignored?: boolean;
    role?: { value: string };
    name?: { value: string };
    childIds?: string[];
    backendDOMNodeId?: number;
  }>;
}

export interface CdpResolveNodeResult extends CdpBase {
  object?: { objectId?: string };
}

export interface CdpGetResponseBodyResult extends CdpBase {
  body?: string;
  base64Encoded?: boolean;
}

export async function cdpSend(tabId: number, method: string, params?: Record<string, unknown>): Promise<CdpBase> {
  return chrome.debugger.sendCommand({ tabId }, method, params) as Promise<CdpBase>;
}

export type DebuggerEventCallback = (source: chrome.debugger.DebuggerSession, method: string, params?: object) => void;

export function waitForLoad(tabId: number, timeoutMs: number): Promise<boolean> {
  return new Promise((resolve) => {
    let settled = false;

    const onEvent: DebuggerEventCallback = (source, method) => {
      if (source.tabId === tabId && method === "Page.loadEventFired") {
        cleanup();
        settled = true;
        resolve(true);
      }
    };

    const timer = setTimeout(() => {
      if (!settled) {
        cleanup();
        resolve(false);
      }
    }, timeoutMs);

    function cleanup() {
      clearTimeout(timer);
      chrome.debugger.onEvent.removeListener(onEvent);
    }

    chrome.debugger.onEvent.addListener(onEvent);
  });
}
