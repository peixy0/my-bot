export interface ConnectionStatus {
  state: "connected" | "connecting" | "disconnected" | "reconnecting" | "error" | "unconfigured";
  detail: string;
}

export interface Scope {
  refs: Record<string, number>;
  activeTabId: number;
  groupId: number;
  windowId: number;
}

export interface NetworkEntry {
  request_id: string;
  url: string;
  method: string;
  headers: Record<string, string>;
  post_data?: string;
  status?: number;
  status_text?: string;
  mime_type?: string;
  response_headers?: Record<string, string>;
  encoded_data_length?: number;
  completed?: boolean;
  error?: string;
}

export interface NetworkBuffer {
  requests: Map<string, NetworkEntry>;
  listener: (source: chrome.debugger.DebuggerSession, method: string, params?: object) => void;
}

export interface RequestFrame {
  type: "request";
  id: string;
  scope_id: string;
  action: string;
  params: Record<string, unknown>;
}

export interface AuthenticatedFrame {
  type: "authenticated";
}

export interface PongFrame {
  type: "pong";
}

export function isRequestFrame(value: unknown): value is RequestFrame {
  return (
    typeof value === "object" &&
    value !== null &&
    (value as Record<string, unknown>).type === "request" &&
    typeof (value as RequestFrame).id === "string" &&
    typeof (value as RequestFrame).action === "string"
  );
}

export function isAuthFrame(value: unknown): value is { type: "authenticated" } {
  return typeof value === "object" && value !== null && (value as Record<string, unknown>).type === "authenticated";
}

export interface ClickInfo {
  x: number;
  y: number;
  w: number;
  h: number;
  isHtml: boolean;
  visible: boolean;
  pointerEventsNone: boolean;
  disabled: boolean;
}

export interface ClickError {
  error: "stale";
}

export interface SetValueResult {
  set?: boolean;
  tag?: string;
  error?: string;
}

export interface AxTreeNode {
  ref: string;
  role?: string;
  name?: string;
  children?: AxTreeNode[];
}

export type Connection = WebSocket & {
  authenticated?: boolean;
  _pongListener?: (event: MessageEvent) => void;
};
