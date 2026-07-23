const statusEl = document.querySelector("#connection-status");
const connectBtn = document.querySelector("#connect");
const disconnectBtn = document.querySelector("#disconnect");
const settingsLink = document.querySelector("#open-settings");

let currentStatus = null;

connectBtn.addEventListener("click", () => {
  void chrome.runtime.sendMessage({ type: "browserBridgeReconnect" }).then(render);
});

disconnectBtn.addEventListener("click", () => {
  void chrome.runtime.sendMessage({ type: "browserBridgeDisconnect" }).then(render);
});

settingsLink.addEventListener("click", (e) => {
  e.preventDefault();
  void chrome.runtime.openOptionsPage();
});

chrome.runtime.onMessage.addListener((msg) => {
  if (msg?.type === "browserBridgeStatusChanged") {
    render(msg.status);
  }
});

void chrome.runtime.sendMessage({ type: "browserBridgeStatus" }).then(render);

function render(status) {
  currentStatus = status;
  const state = status?.state || "error";
  const detail = status?.detail || "";
  const labels = {
    connected: "Connected",
    connecting: "Connecting…",
    reconnecting: "Reconnecting…",
    disconnected: "Disconnected",
    unconfigured: "Not configured",
    error: "Error"
  };
  statusEl.textContent = `${labels[state] || state}${detail ? ": " + detail : ""}`;
  statusEl.className = "status " + state;
  connectBtn.disabled = state === "connected" || state === "connecting" || state === "reconnecting";
  disconnectBtn.disabled = state === "disconnected" || state === "unconfigured";
}
