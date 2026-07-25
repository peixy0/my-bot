const sessionKey = "myBotBrowserScopes";
const connectionKey = "myBotBrowserConnectionEnabled";
const settingKey = "myBotBrowserSettings";
const groupColors = ["blue", "red", "yellow", "green", "pink", "purple", "cyan", "orange", "grey"];
const protocolVersion = 1;
const maxFrameSize = 1024 * 1024;
const maxTextLength = 64 * 1024;
const reconnectInitialDelay = 1000;
const reconnectMaxDelay = 30000;

let socket = null;
let scopes = new Map();
let reconnectTimer = null;
let heartbeatTimer = null;
let reconnectAttempt = 0;
let connectionEnabled = false;
let connectionStatus = { state: "disconnected", detail: "Connect from extension settings to start the browser bridge." };

// Serialize CDP operations so that each request completes before the next
// starts — critical for SPA interactions where ordering matters (type →
// press Enter → snapshot must see the rendered result).
let requestChain = Promise.resolve();


const initialized = Promise.all([restoreScopes(), restoreConnectionEnabled()]).then(reconcileScopes);

chrome.tabs.onCreated.addListener((tab) => {
  void initialized.then(() => adoptOpenedTab(tab));
});

chrome.tabs.onRemoved.addListener((tabId) => {
  void initialized.then(() => removeTab(tabId));
});

chrome.tabs.onActivated.addListener((activeInfo) => {
  void initialized.then(() => setActiveTab(activeInfo.tabId));
});

chrome.debugger.onDetach.addListener((source, reason) => {
  attachedTabs.delete(source.tabId);
});

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === "browserBridgeStatus") {
    return Promise.resolve(connectionStatus);
  }
  if (message?.type === "browserBridgeReconnect") {
    return initialized.then(() => {
      connectionEnabled = true;
      void chrome.storage.session.set({ [connectionKey]: true });
      restartConnection("manual connection");
      return connectionStatus;
    });
  }
  if (message?.type === "browserBridgeDisconnect") {
    return initialized.then(() => {
      connectionEnabled = false;
      void chrome.storage.session.set({ [connectionKey]: false });
      disconnect("Disconnected by user.");
      return connectionStatus;
    });
  }
  if (message?.type === "browserBridgeSettingsChanged") {
    return initialized.then(() => {
      connectionEnabled = false;
      void chrome.storage.session.set({ [connectionKey]: false });
      disconnect("Settings changed. Connect again to use the new endpoint.");
      return connectionStatus;
    });
  }
  return undefined;
});

async function restoreScopes() {
  const stored = await chrome.storage.session.get(sessionKey);
  const restored = stored[sessionKey];
  if (!restored || typeof restored !== "object" || Array.isArray(restored)) {
    scopes = new Map();
    return;
  }
  scopes = new Map(Object.entries(restored).flatMap(([scopeID, scope]) => {
    if (!isScopeID(scopeID) || !scope || typeof scope !== "object" || Array.isArray(scope)) {
      return [];
    }
    const refs = Object.fromEntries(Object.entries(scope.refs || {}).filter(([ref, tabID]) => isRef(ref) && Number.isInteger(tabID) && tabID > 0));
    return [[scopeID, {
      refs,
      activeTabId: Number.isInteger(scope.activeTabId) ? scope.activeTabId : 0,
      groupId: Number.isInteger(scope.groupId) ? scope.groupId : 0,
      windowId: Number.isInteger(scope.windowId) ? scope.windowId : 0
    }]];
  }));
}

async function restoreConnectionEnabled() {
  const stored = await chrome.storage.session.get(connectionKey);
  connectionEnabled = stored[connectionKey] === true;
  if (connectionEnabled) {
    setConnectionStatus("disconnected", "Restoring the connection.");
    void connect("session restore");
  }
}

async function saveScopes() {
  const serializable = {};
  for (const [scopeID, scope] of scopes) {
    serializable[scopeID] = {
      refs: scope.refs,
      activeTabId: scope.activeTabId || 0,
      groupId: scope.groupId || 0,
      windowId: scope.windowId || 0
    };
  }
  await chrome.storage.session.set({ [sessionKey]: serializable });
}

async function reconcileScopes() {
  let changed = false;
  for (const [scopeID, scope] of scopes) {
    for (const [ref, tabID] of Object.entries(scope.refs)) {
      try {
        const tab = await chrome.tabs.get(tabID);
        if (scope.windowId && tab.windowId !== scope.windowId) {
          delete scope.refs[ref];
          changed = true;
        }
      } catch {
        delete scope.refs[ref];
        changed = true;
      }
    }
    const tabIDs = Object.values(scope.refs);
    if (tabIDs.length === 0) {
      scopes.delete(scopeID);
      changed = true;
      continue;
    }
    if (!tabIDs.includes(scope.activeTabId)) {
      scope.activeTabId = tabIDs[0];
      changed = true;
    }
  }
  if (changed) {
    await saveScopes();
  }
}

async function connect(reason = "connect") {
  if (!connectionEnabled) {
    setConnectionStatus("disconnected", "Connect from extension settings to start the browser bridge.");
    return;
  }
  clearTimeout(reconnectTimer);
  reconnectTimer = null;
  const settings = await chrome.storage.local.get(settingKey);
  const config = settings[settingKey] || {};
  const validationError = validateSettings(config);
  if (validationError) {
    setConnectionStatus(config.url ? "error" : "unconfigured", validationError);
    return;
  }
  try {
    const connection = new WebSocket(config.url);
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
    connection.addEventListener("message", (event) => {
      if (isAuthenticationFrame(event.data)) {
        void handleFrame(event.data, connection);
        return;
      }
      requestChain = requestChain.then(() => handleFrame(event.data, connection)).catch(() => {});
    });
    connection.addEventListener("close", () => {
      if (socket === connection) {
        socket = null;
        stopHeartbeat();
        if (opened && !connection.authenticated) {
          connectionEnabled = false;
          void chrome.storage.session.set({ [connectionKey]: false });
          setConnectionStatus("error", "Authentication failed. Connect again.");
          return;
        }
        scheduleReconnect();
      }
    });
    connection.addEventListener("error", () => {
      if (socket === connection) {
        setConnectionStatus("error", "WebSocket error");
      }
      connection.close();
    });
  } catch (error) {
    setConnectionStatus("error", errorMessage(error));
    scheduleReconnect();
  }
}

function startHeartbeat(connection) {
  stopHeartbeat();
  heartbeatTimer = setInterval(() => {
    if (socket === connection && connection.readyState === WebSocket.OPEN) {
      try {
        connection.send(JSON.stringify({ type: "ping" }));
      } catch {
        connection.close();
      }
    }
  }, 20000);
}

function stopHeartbeat() {
  clearInterval(heartbeatTimer);
  heartbeatTimer = null;
}

function scheduleReconnect() {
  if (!connectionEnabled || reconnectTimer) {
    return;
  }
  clearTimeout(reconnectTimer);
  const baseDelay = Math.min(reconnectInitialDelay * 2 ** reconnectAttempt, reconnectMaxDelay);
  reconnectAttempt = Math.min(reconnectAttempt + 1, 30);
  const delay = Math.round(baseDelay * (0.8 + Math.random() * 0.4));
  setConnectionStatus("reconnecting", `Retrying in ${Math.ceil(delay / 1000)} seconds`);
  reconnectTimer = setTimeout(() => void connect("retry"), delay);
}

function restartConnection(reason) {
  clearTimeout(reconnectTimer);
  reconnectTimer = null;
  reconnectAttempt = 0;
  const previous = socket;
  socket = null;
  stopHeartbeat();
  previous?.close();
  void connect(reason);
}

function disconnect(detail) {
  clearTimeout(reconnectTimer);
  reconnectTimer = null;
  reconnectAttempt = 0;
  const previous = socket;
  socket = null;
  stopHeartbeat();
  previous?.close();
  setConnectionStatus("disconnected", detail);
}

function setConnectionStatus(state, detail = "") {
  connectionStatus = { state, detail };
  void chrome.runtime.sendMessage({ type: "browserBridgeStatusChanged", status: connectionStatus }).catch(() => {});
}

function send(frame) {
  if (socket?.readyState === WebSocket.OPEN) {
    try {
      socket.send(JSON.stringify(frame));
    } catch {
      socket.close();
    }
  }
}

async function handleFrame(raw, connection) {
  if (connection !== socket || typeof raw !== "string" || raw.length > maxFrameSize) {
    return;
  }
  let frame;
  try {
    frame = JSON.parse(raw);
  } catch {
    return;
  }
  if (frame.type === "authenticated") {
    connection.authenticated = true;
    reconnectAttempt = 0;
    setConnectionStatus("connected", "Authenticated");
    send({ type: "ready" });
    return;
  }
  if (!isRequestFrame(frame)) {
    return;
  }
  try {
    await execute(frame.scope_id, frame.action, frame.params || {}, frame.id);
  } catch (error) {
    send({ type: "response", id: frame.id, error: errorMessage(error), has_more: false });
  }
}

async function execute(scopeID, action, params, requestID) {
  if (!isPlainObject(params)) {
    throw new Error("request params must be an object");
  }
  switch (action) {
    case "tabs":
      return sendResult(requestID, await listTabs(scopeID));
    case "new_tab":
      return sendResult(requestID, await newTab(scopeID, params.url));
    case "close_tab":
      return sendResult(requestID, await closeTab(scopeID, params.tab_ref));
    case "activate_tab":
      return sendResult(requestID, await activateTab(scopeID, params.tab_ref));
    case "navigate":
      return sendResult(requestID, await navigate(scopeID, params.tab_ref, params.url));
    case "snapshot":
      return sendResult(requestID, await withTab(scopeID, params.tab_ref, (tab) => cdpSnapshot(scopeID, tab.id)));
    case "click":
      return sendResult(requestID, await withTab(scopeID, params.tab_ref, (tab) => cdpClick(scopeID, tab.id, params.element_ref)));
    case "type":
      return sendResult(requestID, await withTab(scopeID, params.tab_ref, (tab) => cdpType(scopeID, tab.id, params.element_ref, params.text)));
    case "press_key":
      return sendResult(requestID, await withTab(scopeID, params.tab_ref, (tab) => cdpPressKey(tab.id, params.key)));
    case "select_option":
      return sendResult(requestID, await withTab(scopeID, params.tab_ref, (tab) => cdpSelectOption(scopeID, tab.id, params.element_ref, params.value)));
    case "wait":
      return sendResult(requestID, await withTab(scopeID, params.tab_ref, async (tab) => {
        await delay(Math.min(Math.max(Number(params.seconds) || 0, 0), 30) * 1000);
        return { tab_ref: refForTab(scopeID, tab.id), waited: Number(params.seconds) || 0 };
      }));
    case "evaluate":
      return sendResult(requestID, await withTab(scopeID, params.tab_ref, (tab) => evaluate(tab.id, params.script)));
    case "inspect":
      return sendResult(requestID, await withTab(scopeID, params.tab_ref, (tab) => cdpGetHTML(tab.id, params.selector)));
    case "scroll":
      return sendResult(requestID, await withTab(scopeID, params.tab_ref, (tab) => cdpScroll(tab.id, params.direction, params.amount)));
    case "back":
      return sendResult(requestID, await withTab(scopeID, params.tab_ref, (tab) => cdpNavigateHistory(tab.id, "back")));
    case "forward":
      return sendResult(requestID, await withTab(scopeID, params.tab_ref, (tab) => cdpNavigateHistory(tab.id, "forward")));
    case "reload":
      return sendResult(requestID, await withTab(scopeID, params.tab_ref, (tab) => cdpReload(tab.id)));
    case "screenshot":
      return withTab(scopeID, params.tab_ref, (tab) => cdpScreenshot(tab.id, params, requestID));
    case "scope_close":
      return sendResult(requestID, await closeScope(scopeID));
    default:
      throw new Error(`unknown browser action: ${action}`);
  }
}

function sendResult(requestID, result) {
  send({ type: "response", id: requestID, data: JSON.stringify(result), has_more: false });
}

async function listTabs(scopeID) {
  const scope = scopes.get(scopeID);
  if (!scope) {
    return { tabs: [] };
  }
  const tabs = [];
  for (const [ref, tabId] of Object.entries(scope.refs)) {
    try {
      const tab = await chrome.tabs.get(tabId);
      tabs.push({ tab_ref: ref, title: tab.title || "", url: tab.url || "", active: scope.activeTabId === tab.id });
    } catch {
      delete scope.refs[ref];
    }
  }
  if (tabs.length !== Object.keys(scope.refs).length) {
    await saveScopes();
  }
  return { tabs };
}

async function newTab(scopeID, url) {
  const scope = await scopeFor(scopeID);
  const options = { url: validateURL(url || "about:blank"), active: false };
  if (scope.windowId) {
    options.windowId = scope.windowId;
  }
  const tab = await chrome.tabs.create(options);
  const ref = await assignTab(scopeID, tab.id);
  return { tab_ref: ref, title: tab.title || "", url: tab.url || "" };
}

async function closeTab(scopeID, tabRef) {
  const tab = await resolveTab(scopeID, tabRef);
  await detachTab(tab.id);
  await chrome.tabs.remove(tab.id);
  return { tab_ref: tabRef, closed: true };
}

async function activateTab(scopeID, tabRef) {
  const tab = await resolveTab(scopeID, tabRef);
  await chrome.tabs.update(tab.id, { active: true });
  const scope = scopes.get(scopeID);
  scope.activeTabId = tab.id;
  await saveScopes();
  return { tab_ref: tabRef, active: true };
}

async function navigate(scopeID, tabRef, url) {
  const tab = await resolveTab(scopeID, tabRef);
  const targetURL = validateURL(url);
  const updated = await chrome.tabs.update(tab.id, { url: targetURL });
  return { tab_ref: refForTab(scopeID, tab.id), url: updated.url || targetURL };
}

async function withTab(scopeID, tabRef, fn) {
  const tab = await resolveTab(scopeID, tabRef);
  return fn(tab);
}

async function resolveTab(scopeID, tabRef) {
  const scope = scopes.get(scopeID);
  if (!scope) {
    throw new Error("browser scope has no tabs");
  }
  if (tabRef !== undefined && tabRef !== null && !isRef(tabRef)) {
    throw new Error("tab_ref is invalid");
  }
  const tabId = tabRef ? scope.refs[tabRef] : scope.activeTabId || firstTabID(scope);
  if (!tabId) {
    throw new Error("browser scope has no tabs");
  }
  if (!Object.values(scope.refs).includes(tabId)) {
    throw new Error("tab is not owned by this browser scope");
  }
  return chrome.tabs.get(tabId);
}

function firstTabID(scope) {
  return Object.values(scope.refs)[0];
}

function refForTab(scopeID, tabID) {
  const scope = scopes.get(scopeID);
  for (const [ref, id] of Object.entries(scope?.refs || {})) {
    if (id === tabID) {
      return ref;
    }
  }
  return "";
}

async function assignTab(scopeID, tabID) {
  const scope = await scopeFor(scopeID);
  const tab = await chrome.tabs.get(tabID);
  if (scope.windowId && scope.windowId !== tab.windowId) {
    throw new Error("tab window does not match this browser scope");
  }
  scope.windowId ||= tab.windowId;
  const existing = refForTab(scopeID, tabID);
  if (existing) {
    return existing;
  }
  const ref = crypto.randomUUID();
  scope.refs[ref] = tabID;
  scope.activeTabId ||= tabID;
  if (!scope.groupId) {
    await createGroup(scopeID, scope);
  } else {
    try {
      await chrome.tabs.group({ tabIds: [tabID], groupId: scope.groupId });
    } catch {
      await createGroup(scopeID, scope);
    }
  }
  await saveScopes();
  return ref;
}

async function createGroup(scopeID, scope) {
  scope.groupId = await chrome.tabs.group({ tabIds: Object.values(scope.refs) });
  await chrome.tabGroups.update(scope.groupId, {
    title: `${shortScope(scopeID)}`,
    color: groupColors[scopes.size % groupColors.length],
    collapsed: false
  });
}

async function scopeFor(scopeID) {
  let scope = scopes.get(scopeID);
  if (!scope) {
    scope = { refs: {}, activeTabId: 0, groupId: 0, windowId: 0 };
    scopes.set(scopeID, scope);
  }
  return scope;
}

async function adoptOpenedTab(tab) {
  if (!tab.openerTabId) {
    return;
  }
  for (const [scopeID, scope] of scopes) {
    if (scope.windowId === tab.windowId && Object.values(scope.refs).includes(tab.openerTabId)) {
      await assignTab(scopeID, tab.id);
      return;
    }
  }
}

async function removeTab(tabID) {
  await detachTab(tabID);
  let changed = false;
  for (const [scopeID, scope] of scopes) {
    for (const [ref, id] of Object.entries(scope.refs)) {
      if (id === tabID) {
        delete scope.refs[ref];
        changed = true;
      }
    }
    if (scope.activeTabId === tabID) {
      scope.activeTabId = firstTabID(scope) || 0;
      changed = true;
    }
    if (Object.keys(scope.refs).length === 0) {
      scopes.delete(scopeID);
      changed = true;
    }
  }
  if (changed) {
    await saveScopes();
  }
}

async function setActiveTab(tabID) {
  for (const scope of scopes.values()) {
    if (Object.values(scope.refs).includes(tabID)) {
      scope.activeTabId = tabID;
      await saveScopes();
      return;
    }
  }
}

async function closeScope(scopeID) {
  const scope = scopes.get(scopeID);
  if (!scope) {
    return { closed: true };
  }
  const tabIDs = Object.values(scope.refs);
  await Promise.all(tabIDs.map((id) => detachTab(id)));
  if (tabIDs.length > 0) {
    await chrome.tabs.remove(tabIDs).catch(() => {});
  }
  scopes.delete(scopeID);
  await saveScopes();
  return { closed: true };
}

const attachedTabs = new Set();

async function ensureAttached(tabID) {
  if (attachedTabs.has(tabID)) {
    return;
  }
  try {
    await chrome.debugger.attach({ tabId: tabID }, "1.3");
    attachedTabs.add(tabID);
  } catch (error) {
    if (errorMessage(error).includes("Another debugger is already attached")) {
      throw new Error("DevTools is open on this tab; close it and retry");
    }
    throw error;
  }
}

async function detachTab(tabID) {
  if (!attachedTabs.has(tabID)) {
    return;
  }
  attachedTabs.delete(tabID);
  await chrome.debugger.detach({ tabId: tabID }).catch(() => {});
}

async function cdpSend(tabID, method, params = {}) {
  return chrome.debugger.sendCommand({ tabId: tabID }, method, params);
}



const snapshotSelector = "a,button,input,textarea,select,[role=button],[role=link],[contenteditable=true]";

async function cdpSnapshot(scopeID, tabID) {
  await ensureAttached(tabID);

  // Get document root nodeId (depth=0: just the root, no tree walk)
  const doc = await cdpSend(tabID, "DOM.getDocument", { depth: 0 });
  const rootNodeId = doc.root.nodeId;

  // Get live nodeIds via CDP querySelectorAll — same CSS selector,
  // same document order as the Runtime.evaluate below, so pairing by
  // index is guaranteed correct.
  const { nodeIds } = await cdpSend(tabID, "DOM.querySelectorAll", {
    nodeId: rootNodeId,
    selector: snapshotSelector
  });

  // Get element text/attributes/visibility via page-context querySelectorAll
  const { result: pageResult } = await cdpSend(tabID, "Runtime.evaluate", {
    expression: `(function() {
      const selector = "${snapshotSelector}";
      const els = document.querySelectorAll(selector);
      const results = [];
      for (const el of els) {
        if (results.length >= 300) break;
        const rect = el.getBoundingClientRect();
        const style = getComputedStyle(el);
        const visible = rect.width > 0 && rect.height > 0 && style.visibility !== "hidden" && style.display !== "none";
        const hiddenType = el.tagName === "INPUT" && (el.type === "hidden" || el.type === "file");
        results.push({
          tag: el.tagName.toLowerCase(),
          text: (el.innerText || el.value || el.getAttribute("aria-label") || "").trim().slice(0, 300),
          type: el.getAttribute("type") || "",
          name: el.getAttribute("name") || "",
          placeholder: el.getAttribute("placeholder") || "",
          visible: visible && !hiddenType
        });
      }
      return JSON.stringify({
        title: document.title,
        url: location.href,
        text: (document.body?.innerText || "").slice(0, 16000),
        elements: results
      });
    })()`,
    returnByValue: true,
    awaitPromise: true
  });

  const pageInfo = JSON.parse(pageResult?.value || "{}");
  const scope = scopes.get(scopeID);
  if (!scope) throw new Error("browser scope has no tabs");

  scope.elementRefs = new Map();
  scope.nextElementRef = 0;

  const elements = [];
  const count = Math.min(nodeIds.length, (pageInfo.elements || []).length);
  for (let i = 0; i < count; i++) {
    const elInfo = pageInfo.elements[i];
    if (!elInfo.visible) continue;
    const ref = ++scope.nextElementRef;
    scope.elementRefs.set(ref, nodeIds[i]);
    elements.push({
      ref: String(ref),
      tag: elInfo.tag || "",
      text: elInfo.text || "",
      type: elInfo.type || "",
      name: elInfo.name || ""
    });
  }

  await saveScopes();
  return {
    title: pageInfo.title || "",
    url: pageInfo.url || "",
    text: pageInfo.text || "",
    elements
  };
}



function resolveNodeId(scopeID, elementRef) {
  const scope = scopes.get(scopeID);
  if (!scope || !scope.elementRefs) {
    throw new Error("element_ref is stale; call browser_snapshot again");
  }
  const ref = Number(elementRef);
  if (!Number.isInteger(ref)) {
    throw new Error("element_ref is invalid");
  }
  const nodeId = scope.elementRefs.get(ref);
  if (!nodeId) {
    throw new Error("element_ref is stale; call browser_snapshot again");
  }
  return nodeId;
}

async function cdpClick(scopeID, tabID, elementRef) {
  await ensureAttached(tabID);
  await chrome.tabs.update(tabID, { active: true }).catch(() => {});
  const nodeId = resolveNodeId(scopeID, elementRef);
  await cdpSend(tabID, "DOM.scrollIntoViewIfNeeded", { nodeId });
  const { quads } = await cdpSend(tabID, "DOM.getContentQuads", { nodeId });
  if (!quads || quads.length === 0) {
    throw new Error("element has no visible quads");
  }
  const content = quads[0];
  const xs = content.filter((_, i) => i % 2 === 0);
  const ys = content.filter((_, i) => i % 2 === 1);
  const x = (Math.min(...xs) + Math.max(...xs)) / 2;
  const y = (Math.min(...ys) + Math.max(...ys)) / 2;
  await cdpSend(tabID, "Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
  await cdpSend(tabID, "Input.dispatchMouseEvent", { type: "mousePressed", x, y, button: "left", clickCount: 1 });
  await cdpSend(tabID, "Input.dispatchMouseEvent", { type: "mouseReleased", x, y, button: "left", clickCount: 1 });
  return { clicked: true };
}

async function cdpType(scopeID, tabID, elementRef, text) {
  if (typeof text !== "string" || text.length > maxTextLength) {
    throw new Error("text is too long");
  }
  await ensureAttached(tabID);
  const nodeId = resolveNodeId(scopeID, elementRef);
  await cdpSend(tabID, "DOM.scrollIntoViewIfNeeded", { nodeId });
  await cdpSend(tabID, "DOM.focus", { nodeId });
  // Activate tab so the page processes keyboard events (Chrome
  // suppresses input on background tabs).
  await chrome.tabs.update(tabID, { active: true }).catch(() => {});
  await cdpSend(tabID, "Input.dispatchKeyEvent", { type: "keyDown", key: "Control" });
  await cdpSend(tabID, "Input.dispatchKeyEvent", { type: "keyDown", key: "a", code: "KeyA" });
  await cdpSend(tabID, "Input.dispatchKeyEvent", { type: "keyUp", key: "a", code: "KeyA" });
  await cdpSend(tabID, "Input.dispatchKeyEvent", { type: "keyUp", key: "Control" });
  await cdpSend(tabID, "Input.dispatchKeyEvent", { type: "keyDown", key: "Backspace", code: "Backspace" });
  await cdpSend(tabID, "Input.dispatchKeyEvent", { type: "keyUp", key: "Backspace", code: "Backspace" });
  for (const char of text) {
    const code = keyCodeForChar(char);
    const vk = char.charCodeAt(0);
    await cdpSend(tabID, "Input.dispatchKeyEvent", { type: "keyDown", key: char, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk });
    await cdpSend(tabID, "Input.dispatchKeyEvent", { type: "char", key: char, code, text: char, unmodifiedText: char, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk });
    await cdpSend(tabID, "Input.dispatchKeyEvent", { type: "keyUp", key: char, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk });
  }
  return { typed: true };
}

function keyCodeForChar(c) {
  if (/^[a-zA-Z]$/.test(c)) return "Key" + c.toUpperCase();
  if (/^\d$/.test(c)) return "Digit" + c;
  if (c === " ") return "Space";
  if (c === ".") return "Period";
  if (c === ",") return "Comma";
  if (c === "-") return "Minus";
  if (c === "=") return "Equal";
  if (c === "/") return "Slash";
  if (c === "\\") return "Backslash";
  if (c === "'") return "Quote";
  if (c === ";") return "Semicolon";
  if (c === "[") return "BracketLeft";
  if (c === "]") return "BracketRight";
  if (c === "`") return "Backquote";
  return "";
}

async function cdpSelectOption(scopeID, tabID, elementRef, value) {
  await ensureAttached(tabID);
  await chrome.tabs.update(tabID, { active: true }).catch(() => {});
  const nodeId = resolveNodeId(scopeID, elementRef);
  const { object } = await cdpSend(tabID, "DOM.resolveNode", { nodeId });
  await cdpSend(tabID, "Runtime.callFunctionOn", {
    objectId: object.objectId,
    functionDeclaration: "(function(value) { this.value = value; this.dispatchEvent(new Event('input', {bubbles:true})); this.dispatchEvent(new Event('change', {bubbles:true})); })",
    arguments: [{ value }],
    returnByValue: true
  });
  return { value };
}

async function cdpPressKey(tabID, key) {
  if (typeof key !== "string" || key.length > 64) {
    throw new Error("key is invalid");
  }
  await ensureAttached(tabID);
  await chrome.tabs.update(tabID, { active: true }).catch(() => {});
  await cdpSend(tabID, "Input.dispatchKeyEvent", { type: "keyDown", key, code: key });
  await cdpSend(tabID, "Input.dispatchKeyEvent", { type: "keyUp", key, code: key });
  return { key };
}

async function evaluate(tabID, script) {
  if (typeof script !== "string" || !script.trim() ) {
    throw new Error("script is required");
  }
  await ensureAttached(tabID);
  const result = await cdpSend(tabID, "Runtime.evaluate", {
    expression: script,
    awaitPromise: true,
    returnByValue: true
  });
  if (result.exceptionDetails) {
    throw new Error(formatException(result.exceptionDetails));
  }
  return result.result?.value ?? null;
}

async function cdpGetHTML(tabID, selector) {
  await ensureAttached(tabID);
  const expression = selector
    ? "(() => { const el = document.querySelector('" + selector.replace(/[`"\\]/g, (c) => "\\" + c) + "'); return el ? el.outerHTML : ''; })()"
    : "document.documentElement.outerHTML";
  const result = await cdpSend(tabID, "Runtime.evaluate", {
    expression,
    awaitPromise: true,
    returnByValue: true
  });
  if (result.exceptionDetails) {
    throw new Error(formatException(result.exceptionDetails));
  }
  return String(result.result?.value ?? "");
}







function shortScope(scopeID) {
  return scopeID.slice(-8);
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function cdpScroll(tabID, direction, amount) {
  const delta = direction === "up" ? -Math.abs(amount || 500) : Math.abs(amount || 500);
  await ensureAttached(tabID);

  // Strategy 1: JS scroll — find the actual scrollable container
  const { result: jsResult } = await cdpSend(tabID, "Runtime.evaluate", {
    expression: `(function() {
      const delta = ${delta};

      // 1. document.scrollingElement (standard)
      const se = document.scrollingElement;
      if (se && se.scrollHeight > se.clientHeight) {
        const before = se.scrollTop;
        se.scrollBy(0, delta);
        return JSON.stringify({method:"scrollingElement",before,after:se.scrollTop,max:se.scrollHeight-se.clientHeight});
      }

      // 2. window (traditional pages)
      const de = document.documentElement;
      if (de.scrollHeight > window.innerHeight) {
        const before = window.scrollY;
        window.scrollBy(0, delta);
        return JSON.stringify({method:"window",before,after:window.scrollY,max:de.scrollHeight-window.innerHeight});
      }

      // 3. Find first scrollable container (SPAs)
      const all = document.querySelectorAll("*");
      for (const el of all) {
        if (el.scrollHeight > el.clientHeight && el.clientHeight > 50) {
          const style = getComputedStyle(el);
          const overflowY = style.overflowY || style.overflow;
          if (overflowY === "auto" || overflowY === "scroll") {
            const before = el.scrollTop;
            el.scrollBy(0, delta);
            return JSON.stringify({method:"element",tag:el.tagName.toLowerCase(),before,after:el.scrollTop,max:el.scrollHeight-el.clientHeight});
          }
        }
      }

      return JSON.stringify({method:"none",scrolled:false});
    })()`,
    returnByValue: true,
    awaitPromise: true
  });

  let info;
  try { info = JSON.parse(jsResult?.value || "{}"); } catch { info = { method: "none", scrolled: false }; }

  // If JS scroll moved the page, done
  if (info.method !== "none" && info.after !== info.before) {
    return { scrolled: true, method: info.method, before: info.before, after: info.after };
  }

  // Strategy 2: mouse wheel fallback — dispatch at viewport center
  const { result: sizeResult } = await cdpSend(tabID, "Runtime.evaluate", {
    expression: "JSON.stringify({w:window.innerWidth,h:window.innerHeight})",
    returnByValue: true
  });
  let vp = { w: 800, h: 600 };
  try { vp = JSON.parse(sizeResult?.value || "{}"); } catch { /* use defaults */ }

  await cdpSend(tabID, "Input.dispatchMouseEvent", {
    type: "mouseWheel",
    x: Math.round(vp.w / 2),
    y: Math.round(vp.h / 2),
    deltaX: 0,
    deltaY: delta
  });

  return { scrolled: true, method: "mouseWheel", delta };
}

async function cdpNavigateHistory(tabID, direction) {
  await ensureAttached(tabID);
  const { currentIndex, entries } = await cdpSend(tabID, "Page.getNavigationHistory");
  const targetIndex = direction === "back" ? currentIndex - 1 : currentIndex + 1;
  if (targetIndex < 0 || targetIndex >= entries.length) {
    throw new Error(direction === "back" ? "no previous page in history" : "no next page in history");
  }
  await cdpSend(tabID, "Page.navigateToHistoryEntry", { entryId: entries[targetIndex].id });
  return { navigated: true, direction };
}

async function cdpReload(tabID) {
  await ensureAttached(tabID);
  await cdpSend(tabID, "Page.reload");
  return { reloaded: true };
}

const screenshotChunkSize = 512 * 1024;

async function cdpScreenshot(tabID, params, requestID) {
  await ensureAttached(tabID);
  const captureParams = { format: "png" };
  if (params.selector) {
    const doc = await cdpSend(tabID, "DOM.getDocument", { depth: 0 });
    const { nodeId } = await cdpSend(tabID, "DOM.querySelector", {
      nodeId: doc.root.nodeId,
      selector: params.selector,
    });
    if (!nodeId) {
      throw new Error(`selector not found: ${params.selector}`);
    }
    const { model } = await cdpSend(tabID, "DOM.getBoxModel", { nodeId });
    if (!model || !model.content) {
      throw new Error("selected element has no visible box");
    }
    const quad = model.content;
    const xs = quad.filter((_, i) => i % 2 === 0);
    const ys = quad.filter((_, i) => i % 2 === 1);
    const x = Math.min(...xs);
    const y = Math.min(...ys);
    const width = Math.max(...xs) - x;
    const height = Math.max(...ys) - y;
    if (width <= 0 || height <= 0) {
      throw new Error("selected element has zero size");
    }
    captureParams.clip = { x, y, width, height, scale: 1 };
  } else if (params.full_page) {
    captureParams.captureBeyondViewport = true;
  }
  const { data } = await cdpSend(tabID, "Page.captureScreenshot", captureParams);
  if (!data) {
    throw new Error("screenshot returned no data");
  }
  for (let i = 0; i < data.length; i += screenshotChunkSize) {
    const chunk = data.slice(i, i + screenshotChunkSize);
    send({ type: "response", id: requestID, data: chunk, has_more: true });
  }
  send({ type: "response", id: requestID, data: "", has_more: false });
}

function formatException(details) {
  let msg = details.text || "script error";
  if (details.exception?.description) {
    const desc = String(details.exception.description).split("\n")[0];
    if (desc && !msg.includes(desc)) {
      msg += ": " + desc;
    }
  }
  if (details.lineNumber !== undefined) {
    msg += ` (line ${details.lineNumber}`;
    if (details.columnNumber !== undefined) msg += `:${details.columnNumber}`;
    msg += ")";
  }
  return msg;
}

function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}

function isPlainObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function isScopeID(value) {
  return typeof value === "string" && value.length > 0 && value.length <= 128;
}

function isRef(value) {
  return typeof value === "string" && value.length > 0 && value.length <= 128;
}

function isRequestFrame(frame) {
  return isPlainObject(frame)
    && frame.type === "request"
    && isRef(frame.id)
    && isScopeID(frame.scope_id)
    && typeof frame.action === "string"
    && frame.action.length > 0
    && frame.action.length <= 64;
}

function isAuthenticationFrame(raw) {
  if (typeof raw !== "string" || raw.length > maxFrameSize) {
    return false;
  }
  try {
    return JSON.parse(raw)?.type === "authenticated";
  } catch {
    return false;
  }
}

function validateSettings(settings) {
  if (!settings.url) {
    return "Configure a WebSocket URL to connect.";
  }
  try {
    const endpoint = new URL(settings.url);
    if (!["ws:", "wss:"].includes(endpoint.protocol)) {
      return "WebSocket URL must use ws:// or wss://.";
    }
    if (endpoint.protocol === "ws:" && !isLocalHostname(endpoint.hostname)) {
      return "Use wss:// for remote endpoints.";
    }
  } catch {
    return "WebSocket URL is invalid.";
  }
  return "";
}

function isLocalHostname(hostname) {
  return hostname === "localhost" || hostname === "127.0.0.1" || hostname === "[::1]";
}

function validateURL(value) {
  if (typeof value !== "string" || !value.trim() || value.length > 8192) {
    throw new Error("url is required");
  }
  if (value === "about:blank") {
    return value;
  }
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new Error("url is invalid");
  }
  if (!["http:", "https:", "file:"].includes(url.protocol)) {
    throw new Error("url must use http://, https://, or file://");
  }
  return url.toString();
}
