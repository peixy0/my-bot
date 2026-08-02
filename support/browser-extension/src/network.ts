import { cdpSend, type CdpGetResponseBodyResult } from "./cdp.js";
import { maxNetworkEntries } from "./constants.js";
import type { NetworkBuffer, NetworkEntry } from "./types.js";

function asRecord(value: object | undefined): Record<string, unknown> {
  return (value ?? {}) as Record<string, unknown>;
}

function str(value: unknown): string {
  return String(value ?? "");
}

export async function networkStart(
  tabId: number,
  networkBuffers: Map<number, NetworkBuffer>,
): Promise<{ started: boolean }> {
  if (networkBuffers.has(tabId)) {
    return { started: true };
  }

  const buffer: NetworkBuffer = {
    requests: new Map(),
    listener: (_source: chrome.debugger.DebuggerSession, method: string, params?: object) => {
      const p = asRecord(params);

      switch (method) {
        case "Network.requestWillBeSent": {
          if (buffer.requests.size >= maxNetworkEntries) return;
          const req = asRecord(p.request as object | undefined);
          buffer.requests.set(str(p.requestId), {
            request_id: str(p.requestId),
            url: str(req.url),
            method: str(req.method),
            headers: (req.headers ?? {}) as Record<string, string>,
            post_data: str(req.postData),
          });
          break;
        }
        case "Network.responseReceived": {
          const entry = buffer.requests.get(str(p.requestId));
          if (!entry) return;
          const resp = asRecord(p.response as object | undefined);
          entry.status = Number(resp.status);
          entry.status_text = str(resp.statusText);
          entry.mime_type = str(resp.mimeType);
          entry.response_headers = (resp.headers ?? {}) as Record<string, string>;
          break;
        }
        case "Network.loadingFinished": {
          const entry = buffer.requests.get(str(p.requestId));
          if (!entry) return;
          entry.encoded_data_length = Number(p.encodedDataLength);
          entry.completed = true;
          break;
        }
        case "Network.loadingFailed": {
          const entry = buffer.requests.get(str(p.requestId));
          if (!entry) return;
          entry.error = str(p.errorText);
          entry.completed = true;
          break;
        }
      }
    },
  };

  chrome.debugger.onEvent.addListener(buffer.listener);
  networkBuffers.set(tabId, buffer);

  await cdpSend(tabId, "Network.enable", {});
  return { started: true };
}

export async function networkStop(
  tabId: number,
  networkBuffers: Map<number, NetworkBuffer>,
): Promise<{ stopped: boolean }> {
  const buffer = networkBuffers.get(tabId);
  if (!buffer) return { stopped: true };

  chrome.debugger.onEvent.removeListener(buffer.listener);
  networkBuffers.delete(tabId);

  await cdpSend(tabId, "Network.disable", {});
  return { stopped: true };
}

export function networkList(
  tabId: number,
  networkBuffers: Map<number, NetworkBuffer>,
): { capturing: boolean; count: number; requests: NetworkEntry[] } {
  const buffer = networkBuffers.get(tabId);
  if (!buffer) return { requests: [], count: 0, capturing: false };

  return {
    capturing: true,
    count: buffer.requests.size,
    requests: Array.from(buffer.requests.values()),
  };
}

export async function networkDetail(
  tabId: number,
  requestId: string,
  networkBuffers: Map<number, NetworkBuffer>,
): Promise<NetworkEntry & { body: string }> {
  const buffer = networkBuffers.get(tabId);
  if (!buffer) throw new Error("network capture not started");

  const entry = buffer.requests.get(requestId);
  if (!entry) throw new Error("request not found");

  let body = "[body unavailable]";
  try {
    const result = (await cdpSend(tabId, "Network.getResponseBody", { requestId })) as CdpGetResponseBodyResult;
    if (result.body) {
      body = result.base64Encoded ? `[base64: ${result.body.length} bytes]` : result.body;
    }
  } catch {
    // skip
  }

  return { ...entry, body: body.slice(0, 64000) };
}
