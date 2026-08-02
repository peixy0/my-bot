# My Bot Browser Bridge — Design

## Architecture

```
┌──────────────┐     WebSocket      ┌──────────────────┐     CDP      ┌────────────┐
│  Go Broker   │ ◄────────────────► │  Service Worker   │ ◄──────────► │  Browser   │
│  (toolset)   │   JSON frames      │  (extension)      │   chrome.    │  Tab       │
│              │                    │                   │   debugger   │            │
└──────────────┘                    └──────────────────┘              └────────────┘
```

- **Go Broker** — 注册 browser_* 工具，把 tool call 通过 WebSocket 转发给 extension
- **Service Worker** — Chrome MV3 extension，WebSocket client + CDP 命令执行
- **Browser Tab** — 通过 `chrome.debugger.attach` + CDP 协议控制

### 模块拆分

| 模块 | 文件 | 职责 |
|------|------|------|
| 入口 | `service-worker.ts` | 消息路由、action dispatch、Chrome 事件监听 |
| 连接 | `connection.ts` | WebSocket 生命周期、心跳、重连 |
| 标签 | `tabs.ts` | Tab/Scope 管理、创建/关闭/导航 |
| 快照 | `snapshot.ts` | AXTree 构建、ref 分配 |
| 操作 | `actions.ts` | click/set_value/press_key/scroll/evaluate/inspect |
| 网络 | `network.ts` | Network capture start/stop/list/detail |
| CDP | `cdp.ts` | attach/detach/send/waitForLoad + 类型定义 |
| 类型 | `types.ts` | 共享接口 |
| 常量 | `constants.ts` | 阈值、图标路径、按键映射 |

---

## 核心设计决策

### 1. AXTree 而非 DOM querySelectorAll

**问题**：旧实现用 `document.querySelectorAll` 扫描 DOM 做快照，三个致命缺陷：
- `<img>` 不匹配选择器 → 图片不可见
- `<div>` 无语义角色 → AI 不知道它是什么
- 没有层级结构 → AI 脑补父子关系

**选择**：切换到 CDP `Accessibility.getFullAXTree`：
- 浏览器计算好的无障碍树，自带 role/name/层级
- 所有元素都有 role（即使只是 generic/StaticText）
- 嵌套输出 → 天然反映页面 DOM 结构

**代价**：`DOM.resolveNode` + `Runtime.callFunctionOn` 翻译 backendNodeId → DOM 引用，比原来多一次 CDP 往返。

**Ignored 节点处理**：AXTree 中 `html` 和 `body` 通常是 ignored 的中间节点（`RootWebArea → html(ignored) → body(ignored) → 实际内容`）。`collectVisibleChildren` 递归穿透 ignored 中间层，确保嵌套树扁平化为可见层级。

### 2. HTMLElement.click() 优先于 CDP 坐标点击

**问题根因**：SPA 页面（小红书、React/Vue 应用）的 `<body>` 高度通常只有几百 px，而实际内容通过 `position: absolute` 或虚拟滚动渲染在 body 之外。CDP `Input.dispatchMouseEvent` 依赖 `document.elementFromPoint` 做 hit-test，在 body 外的坐标返回 `<html>`，点击无效。

**选择**：DOM 级 `HTMLElement.click()` 绕过 hit-test，直接在目标元素上 dispatch click 事件：
- React 17+ 事件委托绑在 root container → 目标元素零 listener 也能拦截
- Vue/Svelte 同理
- 不受 body height/overflow/遮罩层影响

**Fallback**：非 HTMLElement 或 `.click()` throw 时，退回到 CDP 坐标点击。

### 排除式 Actionability 检查（非白名单）

**为什么不做白名单**：
- `addEventListener` 绑定的 listener 不暴露给 DOM API → 无法检测
- React 17+ 事件委托在 root 上，目标元素 listener count = 0 但仍可交互
- 白名单（`INTERACTIVE_TAGS`）会误杀 React Router `<Link>`、自定义按钮等

**当前检查**：只拒绝明确不可交互的元素：
- `pointer-events: none` → 拒绝
- `disabled` / `aria-disabled` → 拒绝
- 其他全放行

### Network Capture

**设计**：
- 数据缓冲区在 service worker 内存中（`Map<tabId, NetworkBuffer>`）
- 监听 CDP `Network.requestWillBeSent` / `responseReceived` / `loadingFinished` / `loadingFailed`
- 200 条上限，response body 截断 64KB
- `network_start`/`network_stop` 幂等：重复调用返回 `{ started/stopped: true }`

### 协议参数命名

Go toolset 对外暴露 `tab`，extension 内部用 `tab_ref`。Extension dispatch 层统一读 `params.tab`（Go 侧 JSON 键），内部 `withTab` / `resolveTab` 继续用 `tab_ref` 语义——外层协议和内层实现解耦。

---

## 输出接口规范

每个 action 返回给 agent 的数据**只包含 agent 需要的信息**，不泄漏实现细节。

| Action | 返回字段 | 不返回 |
|--------|----------|--------|
| `snapshot` | `title`, `url`, `tree` | ~~`text`~~（和 tree 冗余） |
| `click` | `{ clicked: true }` | ~~`method: "dom_click"|"cdp_click"`~~ |
| `scroll` | `{ before, after, max }` | ~~`method`, `tag`~~ |
| `network_start` | `{ started: true }` | ~~`reason: "already capturing"`~~（幂等） |
| `network_stop` | `{ stopped: true }` | ~~`reason: "not capturing"`~~（幂等） |
| `network_list` | `{ capturing, count, requests[] }` | — |
| `network_detail` | `{ ...entry, body }` | — |

---

## 构建管线

```
TypeScript src/*.ts  →  tsc --noEmit (类型检查)
                     →  eslint (lint)
                     →  prettier (格式化)
                     →  esbuild (IIFE bundle → dist/service-worker.js)
```

- **esbuild**：9 个 TS 模块打包为单个 IIFE（`<script>` 注入，不需要 ES module）
- **manifest.json**：不声明 `"type": "module"`（IIFE 不需要）
- **tsconfig.json**：`strict: true`, target ES2022, lib chrome116