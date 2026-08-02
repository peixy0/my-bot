import {
  reconnectInitialDelay,
  reconnectMaxDelay,
  protocolVersion,
  settingKey,
  ICONS_ON,
  ICONS_OFF,
} from "./constants.js";
import { isRequestFrame, isAuthFrame } from "./types.js";
import type { Connection, ConnectionStatus } from "./types.js";

interface ExtensionSettings {
  url?: string;
}

let socket: Connection | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let heartbeatTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectAttempt = 0;
let connectionEnabled = false;
let connectionStatus: ConnectionStatus = {
  state: "disconnected",
  detail: "Connect from extension settings to start the browser bridge.",
};

export function getSocket(): Connection | null {
  return socket;
}

export function getConnectionStatus(): ConnectionStatus {
  return connectionStatus;
}

export function isConnectionEnabled(): boolean {
  return connectionEnabled;
}

export function setConnectionEnabled(enabled: boolean): void {
  connectionEnabled = enabled;
}

export function clearReconnectTimer(): void {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
}

export async function connect(
  reason: string,
  handleFrame: (raw: string, conn: Connection) => Promise<void>,
): Promise<void> {
  if (!connectionEnabled) {
    setConnectionStatus("disconnected", "Connect from extension settings to start the browser bridge.");
    return;
  }
  clearReconnectTimer();

  const settings = await chrome.storage.local.get(settingKey);
  const config: ExtensionSettings = settings[settingKey] || {};
  const validationError = validateSettings(config);
  if (validationError) {
    setConnectionStatus(config.url ? "error" : "unconfigured", validationError);
    return;
  }
  if (!config.url) {
    setConnectionStatus("unconfigured", "No WebSocket URL configured.");
    return;
  }

  try {
    const connection: Connection = new WebSocket(config.url);
    socket = connection;
    let opened = false;
    setConnectionStatus("connecting", reason);

    connection.addEventListener("open", () => {
      if (socket !== connection) {
        connection.close();
        return;
      }
      opened = true;
      connection.send(JSON.stringify({ type: "authenticate", version: protocolVersion }));
      startHeartbeat(connection);
    });

    connection.addEventListener("message", (event: MessageEvent) => {
      if (isAuthFrameString(event.data)) {
        void handleAuthFrame(event.data, connection);
        return;
      }
      let frame: unknown;
      try {
        frame = JSON.parse(event.data as string);
      } catch {
        return;
      }
      if (!isRequestFrame(frame)) return;
      void handleFrame(event.data as string, connection);
    });

    connection.addEventListener("close", () => {
      if (socket === connection) {
        stopHeartbeat(connection);
        if (opened && !connection.authenticated) {
          connectionEnabled = false;
          setConnectionStatus("error", "Authentication failed. Connect again.");
          return;
        }
        scheduleReconnect(handleFrame);
      }
    });

    connection.addEventListener("error", () => {
      if (socket === connection) {
        setConnectionStatus("error", "WebSocket error");
      }
      connection.close();
    });
  } catch (error: unknown) {
    setConnectionStatus("error", errorMessage(error));
    scheduleReconnect(handleFrame);
  }
}

export function restartConnection(
  reason: string,
  handleFrame: (frame: string, conn: Connection) => Promise<void>,
): void {
  clearReconnectTimer();
  reconnectAttempt = 0;
  const previous = socket;
  socket = null;
  stopHeartbeat(previous);
  previous?.close();
  void connect(reason, handleFrame);
}

export function disconnect(detail: string): void {
  clearReconnectTimer();
  reconnectAttempt = 0;
  const previous = socket;
  socket = null;
  stopHeartbeat(previous);
  previous?.close();
  setConnectionStatus("disconnected", detail);
}

export function send(frame: Record<string, unknown>): void {
  if (socket?.readyState === WebSocket.OPEN) {
    try {
      socket.send(JSON.stringify(frame));
    } catch {
      socket.close();
    }
  }
}

function startHeartbeat(connection: Connection): void {
  stopHeartbeat(connection);
  let lastPong = Date.now();

  const pongListener = (event: MessageEvent) => {
    if (event.target !== connection) return;
    try {
      const msg = JSON.parse(event.data as string);
      if (msg.type === "pong") {
        lastPong = Date.now();
      }
    } catch {
      /* ignore */
    }
  };
  connection.addEventListener("message", pongListener);
  connection._pongListener = pongListener;

  heartbeatTimer = setInterval(() => {
    if (socket !== connection || connection.readyState !== WebSocket.OPEN) return;
    if (Date.now() - lastPong > 40000) {
      connection.close();
      return;
    }
    try {
      connection.send(JSON.stringify({ type: "ping" }));
    } catch {
      connection.close();
    }
  }, 20000);
}

function stopHeartbeat(connection: Connection | null): void {
  if (heartbeatTimer) {
    clearInterval(heartbeatTimer);
    heartbeatTimer = null;
  }
  const conn = connection || socket;
  if (conn?._pongListener) {
    conn.removeEventListener("message", conn._pongListener);
    delete conn._pongListener;
  }
}

function scheduleReconnect(handleFrame: (frame: string, conn: Connection) => Promise<void>): void {
  if (!connectionEnabled || reconnectTimer) return;
  clearReconnectTimer();
  const baseDelay = Math.min(reconnectInitialDelay * 2 ** reconnectAttempt, reconnectMaxDelay);
  reconnectAttempt = Math.min(reconnectAttempt + 1, 30);
  const delay = Math.round(baseDelay * (0.8 + Math.random() * 0.4));
  setConnectionStatus("reconnecting", `Retrying in ${Math.ceil(delay / 1000)} seconds`);
  reconnectTimer = setTimeout(() => void connect("retry", handleFrame), delay);
}

async function handleAuthFrame(raw: string, connection: Connection): Promise<void> {
  let frame: unknown;
  try {
    frame = JSON.parse(raw);
  } catch {
    return;
  }
  if (!isAuthFrame(frame)) return;

  connection.authenticated = true;
  reconnectAttempt = 0;
  setConnectionStatus("connected", "Authenticated");
  send({ type: "ready" });
}

function setConnectionStatus(state: ConnectionStatus["state"], detail = ""): void {
  connectionStatus = { state, detail };
  updateIcon(state);
  void chrome.runtime.sendMessage({ type: "browserBridgeStatusChanged", status: connectionStatus }).catch(() => {});
}

function updateIcon(state: ConnectionStatus["state"]): void {
  const icons = state === "connected" ? ICONS_ON : ICONS_OFF;
  const absoluteIcons: Record<string, string> = {};
  for (const [size, path] of Object.entries(icons)) {
    absoluteIcons[size] = chrome.runtime.getURL(path);
  }
  chrome.action.setIcon({ path: absoluteIcons }).catch(() => {});
}

function isAuthFrameString(raw: string): boolean {
  try {
    return isAuthFrame(JSON.parse(raw));
  } catch {
    return false;
  }
}

function validateSettings(config: ExtensionSettings): string | null {
  if (!config.url) return "No WebSocket URL configured.";
  try {
    const u = new URL(config.url);
    if (u.protocol !== "ws:" && u.protocol !== "wss:") {
      return "URL must use ws:// or wss:// protocol";
    }
  } catch {
    return "URL is invalid";
  }
  return null;
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  return String(error);
}
