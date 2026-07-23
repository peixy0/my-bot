const settingKey = "myBotBrowserSettings";
const form = document.querySelector("#settings");
const urlInput = document.querySelector("#url");
const tokenInput = document.querySelector("#token");
const formStatus = document.querySelector("#form-status");
const connectionStatus = document.querySelector("#connection-status");
const saveButton = document.querySelector("#save");
const connectButton = document.querySelector("#connect");
const disconnectButton = document.querySelector("#disconnect");
const tokenToggle = document.querySelector("#toggle-token");

void initialize();

form.addEventListener("submit", (event) => {
  event.preventDefault();
  void save();
});

connectButton.addEventListener("click", () => {
  void connect();
});

disconnectButton.addEventListener("click", () => {
  void disconnect();
});

tokenToggle.addEventListener("click", () => {
  const visible = tokenInput.type === "text";
  tokenInput.type = visible ? "password" : "text";
  tokenToggle.textContent = visible ? "Show" : "Hide";
  tokenToggle.setAttribute("aria-pressed", String(!visible));
});

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === "browserBridgeStatusChanged") {
    renderConnectionStatus(message.status);
  }
});

async function initialize() {
  await load();
  await refreshConnectionStatus();
}

async function load() {
  const stored = await chrome.storage.local.get(settingKey);
  const settings = stored[settingKey] || {};
  urlInput.value = settings.url || "ws://127.0.0.1:8020/browser";
  tokenInput.value = settings.token || "";
}

async function save() {
  try {
    setSaving(true);
    setFormStatus("");
    const parsed = validateEndpoint(urlInput.value);
    await chrome.storage.local.set({
      [settingKey]: { url: parsed.toString(), token: tokenInput.value.trim() }
    });
    renderConnectionStatus(await chrome.runtime.sendMessage({ type: "browserBridgeSettingsChanged" }));
    urlInput.value = parsed.toString();
    setFormStatus("Settings saved. Select Connect to start the bridge.");
    return true;
  } catch (error) {
    setFormStatus(errorMessage(error));
    return false;
  } finally {
    setSaving(false);
  }
}

async function connect() {
  try {
    if (!await save()) {
      return;
    }
    setConnectionBusy(true);
    const status = await chrome.runtime.sendMessage({ type: "browserBridgeReconnect" });
    renderConnectionStatus(status);
  } catch (error) {
    renderConnectionStatus({ state: "error", detail: errorMessage(error) });
  } finally {
    setConnectionBusy(false);
  }
}

async function disconnect() {
  try {
    setConnectionBusy(true);
    const status = await chrome.runtime.sendMessage({ type: "browserBridgeDisconnect" });
    renderConnectionStatus(status);
  } catch (error) {
    renderConnectionStatus({ state: "error", detail: errorMessage(error) });
  } finally {
    setConnectionBusy(false);
  }
}

async function refreshConnectionStatus() {
  try {
    renderConnectionStatus(await chrome.runtime.sendMessage({ type: "browserBridgeStatus" }));
  } catch (error) {
    renderConnectionStatus({ state: "error", detail: errorMessage(error) });
  }
}

function validateEndpoint(value) {
  const parsed = new URL(value.trim());
  if (!["ws:", "wss:"].includes(parsed.protocol)) {
    throw new Error("WebSocket URL must use ws:// or wss://.");
  }
  if (parsed.protocol === "ws:" && !isLocalHostname(parsed.hostname)) {
    throw new Error("Use wss:// for remote endpoints.");
  }
  return parsed;
}

function isLocalHostname(hostname) {
  return hostname === "localhost" || hostname === "127.0.0.1" || hostname === "[::1]";
}

function renderConnectionStatus(status) {
  const state = status?.state || "error";
  const detail = status?.detail || "Unable to read the bridge status.";
  const labels = {
    connected: "Connected",
    connecting: "Connecting",
    reconnecting: "Reconnecting",
    disconnected: "Disconnected",
    unconfigured: "Not configured",
    error: "Connection error"
  };
  connectionStatus.textContent = `${labels[state] || "Unknown status"}: ${detail}`;
  connectionStatus.className = `status ${state === "connected" ? "connected" : state === "error" ? "error" : ""}`;
  connectButton.disabled = state === "connected" || state === "connecting" || state === "reconnecting";
  disconnectButton.disabled = state === "disconnected" || state === "unconfigured";
}

function setSaving(saving) {
  saveButton.disabled = saving;
  saveButton.textContent = saving ? "Saving…" : "Save settings";
}

function setConnectionBusy(busy) {
  connectButton.disabled = busy;
  disconnectButton.disabled = busy;
}

function setFormStatus(message) {
  formStatus.textContent = message;
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}
