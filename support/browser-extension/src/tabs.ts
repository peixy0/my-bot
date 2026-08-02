import { detachTab, cleanupNetworkBuffer } from "./cdp.js";
import type { NetworkBuffer, Scope } from "./types.js";
import { groupColors } from "./constants.js";

const scopes = new Map<string, Scope>();

export function getScopes(): Map<string, Scope> {
  return scopes;
}

export function shortScope(scopeId: string): string {
  return scopeId.slice(-8);
}

export function refForTab(scopeId: string, tabId: number): string {
  const scope = scopes.get(scopeId);
  if (!scope) return "";
  for (const [ref, id] of Object.entries(scope.refs)) {
    if (id === tabId) return ref;
  }
  return "";
}

function firstTabId(scope: Scope): number {
  return Object.values(scope.refs)[0] ?? 0;
}

async function createGroup(scopeId: string, scope: Scope): Promise<void> {
  const groupId = await chrome.tabs.group({ tabIds: Object.values(scope.refs) as [number, ...number[]] });
  scope.groupId = groupId;
  await chrome.tabGroups.update(groupId, {
    title: `${shortScope(scopeId)}`,
    color: groupColors[scopes.size % groupColors.length] as
      "blue" | "red" | "yellow" | "green" | "pink" | "purple" | "cyan" | "orange" | "grey",
    collapsed: false,
  });
}

export async function assignTab(scopeId: string, tabId: number): Promise<string> {
  const scope = await scopeFor(scopeId);
  const tab = await chrome.tabs.get(tabId);
  if (scope.windowId && scope.windowId !== tab.windowId) {
    throw new Error("tab window does not match this browser scope");
  }
  scope.windowId ||= tab.windowId;
  const existing = refForTab(scopeId, tabId);
  if (existing) return existing;
  const ref = crypto.randomUUID();
  scope.refs[ref] = tabId;
  scope.activeTabId ||= tabId;
  if (!scope.groupId) {
    await createGroup(scopeId, scope);
  } else {
    try {
      await chrome.tabs.group({ tabIds: [tabId] as [number, ...number[]], groupId: scope.groupId });
    } catch {
      await createGroup(scopeId, scope);
    }
  }
  return ref;
}

export async function scopeFor(scopeId: string): Promise<Scope> {
  let scope = scopes.get(scopeId);
  if (!scope) {
    scope = { refs: {}, activeTabId: 0, groupId: 0, windowId: 0 };
    scopes.set(scopeId, scope);
  }
  return scope;
}

export async function resolveTab(scopeId: string, tabRef?: string): Promise<chrome.tabs.Tab> {
  const scope = scopes.get(scopeId);
  if (!scope) throw new Error("browser scope has no tabs");
  if (tabRef !== undefined && tabRef !== null && !isRef(tabRef)) {
    throw new Error("tab_ref is invalid");
  }
  const tabId = tabRef ? scope.refs[tabRef] : scope.activeTabId || firstTabId(scope);
  if (!tabId) throw new Error("browser scope has no tabs");
  if (!Object.values(scope.refs).includes(tabId)) {
    throw new Error("tab is not owned by this browser scope");
  }
  return chrome.tabs.get(tabId);
}

export async function withTab<T>(
  scopeId: string,
  tabRef: string,
  fn: (tab: chrome.tabs.Tab) => Promise<T>,
): Promise<T> {
  const tab = await resolveTab(scopeId, tabRef);
  return fn(tab);
}

export async function listTabs(
  scopeId: string,
): Promise<{ tabs: Array<{ tab_ref: string; title: string; url: string; active: boolean }> }> {
  const scope = scopes.get(scopeId);
  if (!scope) return { tabs: [] };
  const tabs = [];
  for (const [ref, tabId] of Object.entries(scope.refs)) {
    try {
      const tab = await chrome.tabs.get(tabId);
      tabs.push({ tab_ref: ref, title: tab.title || "", url: tab.url || "", active: scope.activeTabId === tab.id });
    } catch {
      delete scope.refs[ref];
    }
  }
  return { tabs };
}

export async function newTab(scopeId: string, url: string): Promise<{ tab_ref: string }> {
  const scope = await scopeFor(scopeId);
  const options: chrome.tabs.CreateProperties = { url: validateURL(url || "about:blank"), active: false };
  if (scope.windowId) options.windowId = scope.windowId;
  const tab = await chrome.tabs.create(options);
  const ref = await assignTab(scopeId, tab.id!);
  return { tab_ref: ref };
}

export async function closeTab(scopeId: string, tabRef: string): Promise<{ tab_ref: string; closed: boolean }> {
  const tab = await resolveTab(scopeId, tabRef);
  await detachTab(tab.id!);
  await chrome.tabs.remove(tab.id!);
  return { tab_ref: tabRef, closed: true };
}

export async function navigate(
  scopeId: string,
  tabRef: string,
  url: string,
): Promise<{ tab_ref: string; url: string }> {
  const tab = await resolveTab(scopeId, tabRef);
  const targetURL = validateURL(url);
  const updated = await chrome.tabs.update(tab.id, { url: targetURL });
  return { tab_ref: refForTab(scopeId, tab.id!), url: updated?.url || targetURL };
}

export async function adoptOpenedTab(tab: chrome.tabs.Tab): Promise<void> {
  if (!tab.openerTabId) return;
  for (const [scopeId, scope] of scopes) {
    if (scope.windowId === tab.windowId && Object.values(scope.refs).includes(tab.openerTabId)) {
      await assignTab(scopeId, tab.id!);
      return;
    }
  }
}

export async function removeTab(tabId: number, networkBuffers: Map<number, NetworkBuffer>): Promise<void> {
  await detachTab(tabId);
  cleanupNetworkBuffer(tabId, networkBuffers);
  for (const [scopeId, scope] of scopes) {
    for (const [ref, id] of Object.entries(scope.refs)) {
      if (id === tabId) {
        delete scope.refs[ref];
      }
    }
    if (scope.activeTabId === tabId) {
      scope.activeTabId = firstTabId(scope) || 0;
    }
    if (Object.keys(scope.refs).length === 0) {
      scopes.delete(scopeId);
    }
  }
}

export function setActiveTab(tabId: number): void {
  for (const scope of scopes.values()) {
    if (Object.values(scope.refs).includes(tabId)) {
      scope.activeTabId = tabId;
      return;
    }
  }
}

export async function closeScope(
  scopeId: string,
  networkBuffers: Map<number, NetworkBuffer>,
): Promise<{ closed: boolean }> {
  const scope = scopes.get(scopeId);
  if (!scope) return { closed: true };
  const tabIDs = Object.values(scope.refs);
  await Promise.all(tabIDs.map((id) => detachTab(id)));
  for (const tabId of tabIDs) cleanupNetworkBuffer(tabId, networkBuffers);
  if (tabIDs.length > 0) {
    await chrome.tabs.remove(tabIDs).catch(() => {});
  }
  scopes.delete(scopeId);
  return { closed: true };
}

export interface Tab {
  tab_ref: string;
  title: string;
  url: string;
  active: boolean;
}

function isRef(value: unknown): value is string {
  return typeof value === "string" && value.length > 0;
}

export function validateURL(raw: string): string {
  const s = String(raw).trim();
  if (!s) throw new Error("url is required");
  try {
    const u = new URL(s);
    if (u.protocol !== "http:" && u.protocol !== "https:") {
      throw new Error("only http/https URLs are supported");
    }
    return u.href;
  } catch (e: unknown) {
    if (e instanceof Error && e.message.includes("only http")) throw e;
    const prefixed = `https://${s}`;
    try {
      return new URL(prefixed).href;
    } catch {
      throw new Error(`url is invalid: ${s}`);
    }
  }
}
