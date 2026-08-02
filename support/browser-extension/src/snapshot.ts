import {
  ensureAttached,
  cdpSend,
  type CdpAxTreeResult,
  type CdpResolveNodeResult,
  type CdpEvaluateResult,
} from "./cdp.js";
import { maxAXNodes } from "./constants.js";
import type { AxTreeNode } from "./types.js";

interface AxNode {
  nodeId: string;
  ignored?: boolean;
  role?: { value: string };
  name?: { value: string };
  childIds?: string[];
  backendDOMNodeId?: number;
}

function axNodeToTreeNode(axNode: AxNode, ref: number): AxTreeNode {
  const node: AxTreeNode = { ref: String(ref) };
  const role = axNode.role?.value || "";
  if (role) node.role = role;
  const name = (axNode.name?.value || "").slice(0, 300);
  if (name) node.name = name;
  return node;
}

function buildNodeMap(axNodes: AxNode[]): {
  nodeMap: Record<string, AxTreeNode>;
  refToBackendId: Map<string, number>;
} {
  const nodeMap: Record<string, AxTreeNode> = {};
  const refToBackendId = new Map<string, number>();
  let ref = 0;

  for (const ax of axNodes) {
    if (ax.ignored) continue;
    if (ax.role?.value === "InlineTextBox") continue;
    if (ref >= maxAXNodes) break;
    ref++;

    nodeMap[ax.nodeId] = axNodeToTreeNode(ax, ref);

    if (ax.backendDOMNodeId) {
      refToBackendId.set(String(ref), ax.backendDOMNodeId);
    }
  }

  return { nodeMap, refToBackendId };
}

function buildAxIndex(axNodes: AxNode[]): Record<string, AxNode> {
  const index: Record<string, AxNode> = {};
  for (const ax of axNodes) {
    index[ax.nodeId] = ax;
  }
  return index;
}

function collectVisibleChildren(
  ax: AxNode,
  axIndex: Record<string, AxNode>,
  nodeMap: Record<string, AxTreeNode>,
): AxTreeNode[] {
  const children: AxTreeNode[] = [];
  for (const childId of ax.childIds || []) {
    const childAx = axIndex[childId];
    if (!childAx) continue;

    if (childAx.ignored) {
      children.push(...collectVisibleChildren(childAx, axIndex, nodeMap));
    } else {
      const childNode = nodeMap[childId];
      if (childNode) children.push(childNode);
    }
  }
  return children;
}

function nestChildren(axNodes: AxNode[], nodeMap: Record<string, AxTreeNode>): void {
  const axIndex = buildAxIndex(axNodes);

  for (const ax of axNodes) {
    if (ax.ignored) continue;
    const parent = nodeMap[ax.nodeId];
    if (!parent) continue;

    const children = collectVisibleChildren(ax, axIndex, nodeMap);
    if (children.length > 0) parent.children = children;
  }
}

function findRoot(axNodes: AxNode[], nodeMap: Record<string, AxTreeNode>): AxTreeNode | null {
  const first = axNodes.find((n) => !n.ignored);
  return first ? (nodeMap[first.nodeId] ?? null) : null;
}

async function storeAgentRefs(tabId: number, refToBackendId: Map<string, number>): Promise<void> {
  if (refToBackendId.size === 0) return;

  await cdpSend(tabId, "DOM.enable");

  const resolved: Array<{ ref: string; objectId: string }> = [];
  for (const [ref, backendNodeId] of refToBackendId) {
    if (!backendNodeId) continue;
    try {
      const data = (await cdpSend(tabId, "DOM.resolveNode", { backendNodeId })) as CdpResolveNodeResult;
      if (data.object?.objectId) resolved.push({ ref, objectId: data.object.objectId });
    } catch {
      // skip
    }
  }

  if (resolved.length === 0) return;

  const winData = (await cdpSend(tabId, "Runtime.evaluate", {
    expression: "window",
    returnByValue: false,
  })) as CdpEvaluateResult;

  await cdpSend(tabId, "Runtime.callFunctionOn", {
    functionDeclaration: `function() {
      if (!window.__agentSnapshotRefs) window.__agentSnapshotRefs = {};
      const refs = ${JSON.stringify(resolved.map((r) => r.ref))};
      for (let i = 0; i < arguments.length; i++) {
        window.__agentSnapshotRefs[refs[i]] = arguments[i];
      }
    }`,
    objectId: winData.result?.objectId,
    arguments: resolved.map((r) => ({ objectId: r.objectId })),
    returnByValue: true,
    awaitPromise: true,
  });
}

export interface SnapshotResult {
  title: string;
  url: string;
  tree: AxTreeNode;
}

export async function cdpSnapshot(tabId: number): Promise<SnapshotResult> {
  await ensureAttached(tabId);

  await cdpSend(tabId, "Accessibility.enable");
  const axData = (await cdpSend(tabId, "Accessibility.getFullAXTree", {})) as CdpAxTreeResult;
  const axNodes = (axData.nodes ?? []) as AxNode[];
  if (axNodes.length === 0) {
    throw new Error("snapshot failed: empty AXTree");
  }

  const { nodeMap, refToBackendId } = buildNodeMap(axNodes);
  nestChildren(axNodes, nodeMap);
  const root = findRoot(axNodes, nodeMap);

  await storeAgentRefs(tabId, refToBackendId);

  const pageData = (await cdpSend(tabId, "Runtime.evaluate", {
    expression: "JSON.stringify({ title: document.title, url: location.href })",
    returnByValue: true,
  })) as CdpEvaluateResult;

  const pageInfo: { title?: string; url?: string } = JSON.parse(String(pageData.result?.value ?? "{}"));

  return {
    title: pageInfo.title || "",
    url: pageInfo.url || "",
    tree: root ?? { ref: "0" },
  };
}
