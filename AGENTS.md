# AGENT.md — Development Guide for Coding Agents

This document guides AI coding agents working on this project. It covers the design philosophy, architecture, conventions, intentional choices, known issues, and development rules.

## Project Overview

An event-driven AI agent framework in Go. It connects messaging platforms (Feishu/Lark, WebSocket, WeChat) to OpenAI-compatible LLMs with an extensible tool system. Supports autonomous operation via heartbeats, cron scheduling, subagent delegation, and a local HTTP/WebSocket WebUI.

**Module:** `my-bot`
**Go version:** 1.26
**Entry point:** `cmd/bot/main.go`

## Design Philosophy

These are the core principles we hold the codebase to. They are load-bearing — please don't drift from them without good reason.

- **Event-driven over shared state.** Components communicate via channels and events, not shared mutable state. The Scheduler's single-goroutine `dispatch()` is the canonical pattern: ownership of `sessions` belongs to one goroutine, no lock needed. Prefer this model when adding new components.
- **Locks are a last resort.** A `sync.Mutex` is an admission that we couldn't structure the code as a single owner. Before adding one, ask: can this state live behind a channel, in a single goroutine, or be made immutable? Locks are acceptable only at boundaries where external libraries hand us callbacks on their own goroutines.
- **Interface-driven boundaries for testability.** Every cross-package collaborator is an interface (`CompletionClient`, `Outbound`, `Runtime`, `Toolset`, `Orchestrator`). Don't add concrete-to-concrete dependencies between sibling packages under `internal/`.
- **Composable prompts and tools.** Prompts compose workspace `.md` files via `promptBase`; tools register into a `Registry`. Extending the system means adding implementations, not modifying the core.
- **Fail loudly at boundaries, gracefully inside.** Errors at I/O boundaries (Feishu HTTP, file reads, LLM calls) get logged at `slog.Warn` or higher. Internal invariant violations should be unreachable, not silently swallowed.

## Architecture

```
cmd/bot/main.go          ← entry point, wiring
internal/
  config/config.go       ← YAML-based configuration (sessions, model presets, overrides)
  events/events.go       ← event types & Outbound interface
  inbox/inbox.go         ← generic Inbox[T] interface + Memory[T] channel-backed impl
  engine/
    scheduler.go         ← event dispatcher, session lifecycle, slash commands
    session.go           ← per-chat session owner for tools, cron, and shutdown
    worker.go            ← per-chat conversation logic and event loop
    cron.go              ← cron job loading & scheduling
    agent.go             ← LLM agent loop, abort, context compression
    loop.go              ← AgentLoop (owns Conversation only)
    orchestrator.go      ← tool dispatch, response strategies, in-loop input drain
    subagent.go          ← agent/fleet subagent toolset + task controller
    registry.go          ← NewSessionRegistry / NewSubagentRegistry helpers
  llm/
    prompt.go            ← composable system prompt builders (Main/Heartbeat/Cron/Subagent)
    client.go            ← shared types: ChatMessage, ToolCall, CompletionRequest/Response, Conversation
    openai.go            ← OpenAI-compatible provider (raw net/http, retry, streaming)
  messaging/
    inbound.go           ← Inbound interface
    websocket/           ← WebSocket outbound adapter (subpackage)
      outbound.go        ← events.Outbound impl + platform-specific tools
    feishu/              ← Feishu/Lark adapter (own subpackage)
      feishu.go          ← Config + dedup window constants
      inbound.go         ← webhook handler (incl. text/image/parent-context)
      outbound.go        ← events.Outbound impl with streaming-card output
      tools.go           ← add_reaction, send_image, send_file
    wechat/              ← WeChat iLink bot adapter (own subpackage)
      wechat.go          ← Config, HTTP client, dedup constants
      inbound.go         ← QR login + long-poll + dedup
      outbound.go        ← events.Outbound impl (chunked send)
    dedup/               ← generic dedup window (channel-as-single-owner pattern)
  runtime/
    runtime.go           ← Runtime interface (Execute, Spawn, ReadFile, etc.) + shared helpers
    host.go              ← local bash execution
    container.go         ← podman/docker execution
  tools/
    contract.go          ← ToolSchema, ToolResult, PreparedTool, ToolPreparer protocol types
    registry.go          ← Registry implementation + Toolset interface + MarshalResult
    toolbox.go           ← DefaultToolset (filesystem / grep / glob / web / vision / skills)
    command.go           ← CommandToolset (run_command / get_task / await_task / list_tasks / kill_task / write_to_task)
    skill.go             ← SkillLoader (frontmatter .md files, use_skill tool)
    search.go            ← WebSearch
    fetch.go             ← HTTP fetch with optional proxy
    format.go            ← read_file / skill / task snapshot formatters
    markdown.go          ← ParseFrontmatter
  tasks/                 ← event-driven TaskManager, drivers, output retention
    types.go             ← Snapshot, Driver/Controller interfaces, Emitter
    manager.go           ← single-owner Manager loop
    driver.go            ← NewProcessDriver (per-task goroutines reporting into Manager)
  util/
    encode.go            ← ToJSON / ToJSONIndent (HTML escape off)
  api/
    server.go            ← HTTP + WebSocket server (chat.html frontend)
```

### Data Flow

```
Inbound (Feishu / WebSocket / WeChat / cron / heartbeat / subagent)
  → AgentEvent → inbox.Inbox[AgentEvent] (chan)
    → Scheduler.dispatch  (single goroutine)
      → chatSession.publishEvent / publishMessage  (per-chat inbox)
        → ConversationWorker.Run  (per chat, own goroutine)
          → Orchestrator + Agent.Run()  (LLM ↔ tools)
            → Outbound.Send / SendDelta / SendFinal  (reply to user)
```

### Concurrency Map

Every long-lived goroutine and what it owns. This is the single source of truth — when you add a goroutine, update this table.

| Goroutine | Started by | Owns / Touches | Synchronization |
|---|---|---|---|
| `Scheduler.Run` | main | `sessions` map; reads `agentInbox` | none — single owner |
| `chatSession.run` (per chat) | Scheduler | session shutdown path, worker lifetime, `CronWorker.Stop` | none within session |
| `ConversationWorker.Run` (per chat) | chatSession | conversation state, `Events` chan, `MessageInbox` chan, `abortCh` | none within worker |
| `cron.Cron` internal goroutines | `CronWorker.scheduler.Start()` | invokes registered funcs that publish to `worker.Events` | callbacks only touch the inbox; no shared map access |
| `tasks.Manager` loop | `tasks.NewManager` | task table, state transitions, retention, `shutdownDone` | single owner goroutine + request channel |
| Per-task pumps | `tasks.NewProcessDriver` | per-task stdin/stdout/stderr bridging and exit reporting | per-task goroutines reporting into `tasks.Manager` |
| Subagent runner goroutine | `subagentRunner.startAgentTask` | each fleet child: builds its own `tasks.Manager`, registers registry, runs `AgentLoop` | lifecycles bound to driver task context |
| Feishu dedup | Feishu inbound | `expires` map + `order` slice | single-owner goroutine, channel-fed (see `messaging/dedup/dedup.go`) |
| WeChat dedup | WeChat inbound | same pattern as Feishu (reuses `messaging/dedup`) | single-owner goroutine |
| Feishu inbound `Run` | main | `larkws` long-poll client, dedup goroutine | cancels when `ctx` done |
| Feishu text/image/post/file processing | Feishu inbound | fire-and-forget per-message goroutine; post downloads at most four images sequentially; files are downloaded and written through `Runtime.WriteTmpFile` | publishes through `inbox.Publish` with timeout |
| WeChat inbound `Run` | main | long-poll loop, dedup, QR login | cancels when `ctx` done |
| WeChat message processing | WeChat inbound | fire-and-forget per-message goroutine; enqueues events | publishes through `inbox.Publish` |
| API server `Run` | main | `http.Server` + WS handler per connection | ctx cancel triggers graceful shutdown (10s) |
| WebSocket per-conn ping loop | API server WS handler | WS keepalive ping ticker | ctx cancel ends loop |
| Browser extension broker loop | main | active extension connection and request/response correlation | single owner goroutine; HTTP handlers only forward connection events and extension responses |
| Browser extension WS handler | Browser broker HTTP server | one Chrome extension WebSocket read loop | forwards frames to broker loop; exits on disconnect or context cancellation |
| Heartbeat `time.AfterFunc` | ConversationWorker | non-blocking send to worker `Events` | timer is `Stop()`-ed on shutdown |
| OpenAI retry loop | `OpenAIProvider.Complete` | exponential backoff (≤ 99 attempts, max 300s) | ctx cancel returns early |

### Key Interfaces

| Interface | Package | Purpose |
|-----------|---------|---------|
| `CompletionClient` | `llm` | LLM provider contract: `Complete(ctx, CompletionRequest) (CompletionResponse, error)` |
| `Orchestrator` | `engine` | Mode-specific output, prepared-tool dispatch, and in-loop input strategy |
| `SystemPrompt` | `llm` | Composable prompt builder (`Build(ctx) string`) |
| `Runtime` | `runtime` | Execution environment abstraction (Execute, Spawn, ReadFile, EditFile, Glob, OSInfo, ...) |
| `Outbound` | `events` | `Send`, `SendDelta`, `SendFinal`, `StartThinking`, `EndThinking` |
| `Toolset` | `tools` | `Register(r *Registry)` — every cross-package tool registration goes through this |
| `Inbound` | `messaging` | `Run(ctx) error` — pushes events into the shared inbox |
| `inbox.Inbox[T]` | `inbox` | Generic pub/sub channel: `Publish`, `Receive`, `TryPublish`, `TryReceive`, `Close`, `Len`, `Cap` |
| `tasks.Driver` / `tasks.Controller` | `tasks` | Driver starts a task; Controller writes input / kills it |

Platform-specific tools are not a separate interface — each `Outbound` simply implements `tools.Toolset` itself, and `llm/registry.go:registerOutboundTools` does the type assertion. See `messaging/feishu/tools.go` and `messaging/websocket/outbound.go` for the pattern.

### Orchestrator Variants

- **HumanInputOrchestrator** (`engine/orchestrator.go`) — Interactive conversation; sends deltas via `SendDelta`, drains in-loop user input via `MaybeInterrupt` after each tool dispatch and re-injects it as a `user` message after all tool results and tool followups. Vision is opted in per-call via `.WithVision(...)`.
- **BackgroundOrchestrator** (`engine/orchestrator.go`) — Heartbeat / cron; suppresses output ending with `NO_REPORT` (the `Send` is skipped entirely, no fallback message).
- **SubagentOrchestrator** (`engine/orchestrator.go`) — Isolated execution; streams content deltas to a `tasks.Emitter` (which become the task's captured output) and drains a private input channel via `MaybeInterrupt` between tool dispatches.

## Intentional Design Choices

These are non-obvious decisions worth preserving. If you're tempted to "fix" one of these, read the rationale first.

- **Single-goroutine Scheduler dispatch.** `Scheduler.sessions` is deliberately unprotected by mutex. The invariant: only `Run()` reads/writes it. If you ever need access from elsewhere, send an event into the inbox rather than introducing a lock. The map is documented in `scheduler.go`.
- **Per-chat ConversationWorker isolation.** Each chat has its own goroutine, its own `Events` channel, its own `MessageInbox`, and its own conversation state. No cross-chat sharing means no cross-chat synchronization.
- **Per-arrival Outbound instances.** Each inbound message creates a fresh `Outbound` instance (`feishu/inbound.go:65`). The `Outbound`'s mutable streaming state (`partial`, `stream`) is owned exclusively by the worker goroutine that processes the event — no concurrent access. The `Send` method called from inbound goroutines (`feishu/inbound.go:81`) only touches the `client` (lark SDK, which is goroutine-safe); it does not touch `partial` or `stream`. If you ever need a shared Outbound, add a mutex — but the current per-arrival pattern avoids that entirely.
- **Per-arrival Registry rebuild.** `BuildRegistry(sender)` is called on every `processText`/`processImage`/`runBackground` invocation (`worker.go:208, 236, 264`). This means the outbound toolset (and its `send_image`/`send_file`/`add_reaction` tools) is always built from the current message's `Sender`. There is never a conflict between same-named tools from different platforms — the registry reflects only the active sender. The cost is acceptable because Registry construction is cheap (map allocations) and happens at human interaction speed (not per-token).
- **Session owns resources; worker borrows them.** `chatSession` owns `tasks.Manager`, `CommandToolset`, cron lifecycle, and shutdown. `ConversationWorker` uses these resources through `sessionTools` and should not become an owner again.
- **Per-session config isolation.** `Scheduler` calls `cfg.ForSession(chatID)` to produce a per-session Config (value-copied at the top level, with the per-chat `sessions` override applied, and any matching model preset applied to the `LLMConfig`). Each `chatSession` and its single `ConversationWorker` share this copy; `/model`, `/vision`, etc. changes affect only that session. Sessions never share Config instances. **See Invariants for the shallow-copy caveats.**
- **Live user input uses a separate in-loop inbox, not the worker queue.** While an agent loop is running, ordinary text/images are published into `ConversationWorker.MessageInbox` via `TryPublish` and drained by `HumanInputOrchestrator` after each tool dispatch — non-blocking with no fallthrough. `/queue` wraps the input in `QueuedInputEvent` and dispatches it as a `WorkerEvent` on `Events` for handling after the current loop finishes.
- **Three orchestrator variants instead of mode flags.** Strategy pattern keeps each orchestrator's responsibility crisp; we'd rather grow a fourth variant than feature-flag an existing one. Some methods are empty (e.g. `BackgroundOrchestrator.OnContentDelta`) — this is an acceptable trade-off for the clarity of one-type-per-mode over a shared struct with behavior flags.
- **Raw `net/http` for LLM calls, no SDK.** The OpenAI-compatible provider serializes messages and tools as `map[string]any`, consumes streaming chat-completion events directly, applies exponential-backoff retry on transient errors (HTTP > 403 and `io.ErrUnexpectedEOF` up to 99 attempts), and exposes `reasoning_content` and `extra_body` passthrough fields. Don't reintroduce `openai-go`.
- **`parallel_tool_calls: true` is enabled in every request.** The OpenAI-compatible provider opts into parallel calls at the wire level; `engine/orchestrator.go:runDispatch` further serializes non-`Parallel` tools behind a per-call mutex. All non-parallel tools in a batch share one mutex — this means unrelated non-parallel tools are unnecessarily serialized against each other, not just against their own kind. This is a known suboptimality; the correctness property (non-parallel tools never overlap) is preserved, and the performance impact is negligible in current tool sets where few tools are marked non-parallel. If this becomes a bottleneck, restructure into per-tool-name mutexes or sequential-then-parallel group execution.
- **Tool errors are LLM messages; only preparation failures skip.** Tool preparers deserialize and validate arguments once, then return a `PreparedTool` whose `Execute` closure captures the validated values. Execution must wrap every business/runtime error the LLM should see into `tools.ErrorResult(err)` and return it with `nil` error. A preparer returns an error only for malformed or invalid model-supplied arguments; `Agent.Run` skips those calls and omits them from the replayed assistant tool-call list. `runDispatch`'s `errgroup` goroutines always `return nil` because execution failures are represented as `CallOutcome` values. Do not change `execOne` to forward execution errors through `errgroup` — that would abort the entire tool batch on a single tool's business error.
- **Composable system prompts.** Prompts compose workspace `.md` files (`USER.md` / `PERSONA.md` / `RULES.md` / `CONTEXT.md` / `TOOLS.md`, plus per-mode `HEARTBEAT.md` / `CRON.md`); behavior is data-driven, not code-driven. New prompts embed `promptBase` and declare their section list, not file-reading logic. `SubagentPrompt` is the exception: it only reads `TOOLS.md` plus the caller-supplied `extra` block (subagents start without the main agent's persona/rules).
- **`Runtime` abstraction.** Lets the same tools execute on host bash or in a podman/docker container with no caller changes. Both runtimes expose the full `Runtime` interface including `OSInfo` (host: `runtime.GOOS`/`runtime.GOARCH`/`os.Getwd()`; container: `uname -sm && pwd`). The `Runtime` interface is intentionally monolithic — consumers that need only a subset (e.g. messaging outbound only needs `ReadRawBytes`) currently depend on the full interface. This couples messaging to runtime, which is a known debt (see Architecture Debt section). Do not extract smaller interfaces unless the refactoring also addresses the tool-registration boundary that necessitates `Runtime` in outbound.
- **Messaging outbound holds `Runtime` for platform tools.** `feishu.Outbound` and `websocket.Outbound` both hold a `runtime.Runtime` field used by `send_image`/`send_file` prepared execution closures to call `ReadRawBytes`. This creates a `messaging → runtime` dependency that is **intentionally tolerated** for pragmatic reasons: execution needs file bytes and the outbound is the natural scope for platform-specific tool registration. Do not remove `rt` from outbound without also redesigning the outbound-tool registration mechanism.
- **Task-first asynchronous execution.** Commands, delegated agents, and fleets all create `Task` objects (`task_id = task-<N>`) and return `task_id` immediately. Progress, output, and termination flow through `internal/tasks`. Flat namespace: every spawned unit — shell command, subagent, fleet child — is just a task with its own controller and lifecycle.
- **Event-driven task ownership.** `tasks.Manager` is the single owner of task state, using a request/event channel rather than mutexes. Subagents construct their own private `tasks.Manager` inside the driver goroutine so they can be torn down with the rest of their work; the parent session never sees those children — they show up only as the parent task's output.
- **The agent loop owns tool preparation and description timing.** `Agent.Run` prepares the complete tool-call batch, derives the user-visible descriptions, invokes `Orchestrator.BeforeToolUse`, and only then asks the orchestrator to dispatch the prepared calls. Human sessions append descriptions to the output stream; background and subagent orchestrators keep the hook silent. Descriptions never enter the replayed assistant content.
- **Tool dispatch is selectively parallel; caller assembles conversation.** `runDispatch()`: a single prepared tool call runs inline; multiple calls run under `errgroup`, with parallel-marked tools dispatched without serialization and the rest serialized behind a per-call mutex. The returned `[]CallOutcome` is aligned 1:1 with the input `calls`; the caller (`Agent.Run`) iterates outcomes, builds `assistantMsg.ToolCalls` from the successful ones, then appends messages in OpenAI-safe order: all tool result messages first, all multimodal tool followup `user` messages second, and any `MaybeInterrupt(ctx)` user injection last. Skipped entries (where `outcome.Err` is non-nil, currently only preparation failures) vanish from both the assistant message and the conversation — the LLM has no memory of the failed call.
- **HostRuntime commands run under a login shell.** `HostRuntime.Execute` and `Spawn` intentionally use `bash -l -c` so locally configured shell environments, proxies, PATH entries, Go toolchains, and language managers are available to tools. Login profiles may change the child process's working directory (for example by `cd`-ing to `/workspace`), so runtime tests and tools that require a precise search root must pass explicit absolute paths/patterns instead of relying only on `os.Chdir` in the parent Go process. `OSInfo` reports the parent process working directory; shell commands may still observe a profile-adjusted directory.
- **Generic `Inbox[T]`.** `internal/inbox` is a tiny type-parametric channel wrapper. All event queues (`Scheduler`'s `agentInbox`, `ConversationWorker.Events`, `ConversationWorker.MessageInbox`, `CronWorker`'s inbox, etc.) flow through it. The `ErrFull`/`ErrClosed` sentinels and `TryPublish`/`TryReceive` shape every drop-on-full back-pressure decision.
- **Platform tools piggyback on `Outbound` as `Toolset`.** `feishu.Outbound` and `websocket.Outbound` both implement `tools.Toolset.Register(*Registry)`. The session registry does a type assertion in `registerOutboundTools` and calls it; no separate `ToolRegistrar` interface. Adding platform tools means adding methods to the existing `Outbound` impl, not introducing a new abstraction.
- **`Registry.Register` silently overwrites duplicate names.** The current system avoids conflicts because each registry is built per-arrival with a unique sender. Conflicting names from different Toolsets (e.g. two Toolsets both registering `read_file`) would silently overwrite. This is acceptable in the current architecture but fragile — if Toolset proliferation makes duplicate registration plausible, add a `panic` on duplicate names at `Register` time.
- **Registered tool preparers and prepared execution functions are non-nil.** Every `Registry.Register` call provides a valid `ToolPreparer`, and every successful preparation returns a `PreparedTool` with a valid `Execute` function. Dispatch code trusts this invariant and does not add defensive nil checks.
- **`tasks.Manager` uses `chan any` with type switches.** Go lacks sum types; a unified request channel is the simplest way to maintain single-goroutine ownership over the task table while supporting 10+ operation types. Type safety is enforced by unexported request structs. This is a deliberate trade-off: a typed channel per operation would require 10+ channels and a multi-way select, which is more complex and harder to extend.
- **Dedup goroutine fails open on timeout.** `dedup.Check` returns `true` (not-a-duplicate) if it cannot reach the dedup goroutine within 1 second. Under shutdown, the dedup goroutine may have already exited; failing open (allowing a possible duplicate) is safer than failing closed (silently discarding a user message). A duplicate message causes at most a duplicate reply; a silently dropped message is invisible to the user and unrecoverable.

## Conventions

### Code Style
- Standard Go style. No external linter config — follow `gofmt`.
- `internal/` for all non-main packages. Nothing is exported outside the module.
- Error wrapping with `fmt.Errorf("context: %w", err)` — use sparingly, only at package boundaries.
- `slog` for structured logging. `Error` for failures, `Warn` for degraded paths, `Info` for lifecycle events, `Debug` for verbose tracing (request bodies, retry attempts, tool call args).

### No Comments In Code

This codebase deliberately holds a **no-comments policy**. Code expresses *what*; identifiers, types, and structure should make *why* obvious enough.

- Do not add comments to explain what code does — rename, restructure, or split the function instead.
- Do not add doc comments to exported types/functions just to satisfy linters. The package-level conventions in this document apply globally.
- Do not leave TODO/FIXME notes in code; open a tracked entry in "Known Issues & Required Fixes" or the relevant Concurrency Map row.
- Do not write comments that point at other files, document hidden invariants, or restate design philosophy. **Those belong in AGENT.md.** When you discover an invariant that isn't obvious from the code, add it to the appropriate section here (Concurrency Map, Intentional Design Choices, or Invariants below) rather than annotating the code.
- The only exceptions are: (a) `//go:` directives, (b) struct-tag-adjacent JSON examples in tests where they materially aid debugging, and (c) license headers if ever required.

If a function genuinely requires a comment to be understandable, that's a signal the function should be smaller, better-named, or restructured.

### Invariants (kept here instead of in code)

These are the load-bearing invariants that an earlier draft expressed via inline comments. They are listed here so the code stays clean and the canonical statement is in one place.

- `Scheduler.sessions` is accessed only from the `Run()` goroutine (via `dispatch` / `handleSlashCommand` / `handleCronCommand`, all called inline). No mutex. If you ever need cross-goroutine access, route it through the event inbox.
- `CronWorker.jobs` is mutated only from the Scheduler goroutine. The `cron.Cron` library invokes registered funcs on its own goroutines, but those funcs only call `workerBox.TryPublish` — they never touch the `jobs` map. No mutex.
- `messaging.dedup` is the canonical example of the channel-as-single-owner pattern: one goroutine owns the `expires` map and `order` slice; all callers reach it via the request channel `dedup.in`. The goroutine dies cleanly when the inbound context is cancelled. If `dedup.check` cannot reach the goroutine within 1s, it fails open (returns true) — under shutdown we'd rather risk a duplicate than a deadlock.
- `ConversationWorker.heartbeatTimer` is a `time.AfterFunc` that may outlive normal event processing. It is `Stop()`-ed at the top of every event-loop iteration. Session teardown cancels the worker context, which ends the loop and triggers resource shutdown in `chatSession`.
- `ConversationWorker.cfg` is a per-worker `Config` clone; model and vision changes affect only that worker. The base `Agent` is shared and never forked — `http.Client` is thread-safe. **Shallow-copy caveat: `ForSession` does `merged := *c` (top-level value copy), so `merged.LLM` / `merged.Context` are independent value copies. However, any map fields within them (notably `LLM.ExtraBody` and `Models`) are reference copies — they share the underlying map with the global config or the model preset entry. This is safe ONLY because no code mutates these maps after startup. If you ever write to `cfg.LLM.ExtraBody` or `cfg.Models`, you MUST deep-copy first with `maps.Clone()` or equivalent. Model preset `ApplyTo` assigns `target.ExtraBody = m.ExtraBody` (reference copy) — same rule applies.**
- `chatSession` owns `sessionTools`, `CronWorker`, and the only shutdown path for `tasks.Manager`. `ConversationWorker` must not call `tasks.Manager.Shutdown()` directly.
- Feishu inbound's per-message processing goroutines (`processTextMessage`, `processImageMessage`, `processPostMessage`, `processFileMessage`) and WeChat inbound's `processMessage` are fire-and-forget — every error path logs at `Warn` and either retries (WeChat QR status) or surfaces a single user-visible failure message via `outbound.Send`. `processPostMessage` downloads at most four images sequentially while it builds one `ImageInputEvent`, so a failed download can be represented by its marker in place without post-processing the assembled text. `processFileMessage` stores bytes through `Runtime.WriteTmpFile` and publishes the returned path and original name as a `TextInputEvent`; these temp files follow the existing runtime policy and are not automatically removed. Don't add new fire-and-forget paths without the same discipline.
- The OpenAI provider's retry loop is bounded by `maxAttempts = 99` with exponential backoff capped at 300s. It only retries on `io.ErrUnexpectedEOF` and HTTP statuses > 403. Adding new retryable conditions must remain conservative — we'd rather surface a 4xx error to the user than loop on a bad request.

### Configuration
- All config via a single YAML file (default `./config.yaml`), loaded once in `config.Load()`.
- Defaults in `config.go`. New settings: add field to `Config` struct with `yaml` tag, add default in `defaultConfig()`, add validation in `Validate()`.
- `LLMConfig` exposes `top_k`, `context_window`, `vision`, `extra_body`, and the standard `temperature`/`top_p` for non-OpenAI providers. `Config.Models` is a map of named presets (`ModelConfig`) that override `LLMConfig` fields when the chosen model name matches.
- `ForSession(chatID)` value-copies the global config (top-level struct copy via `merged := *c`), applies the per-chat `sessions[chatID]` override (`LLMOverride` / `ContextOverride`), then applies the matching model preset. Map fields (`Models`, `LLM.ExtraBody`) are reference-copied — they share underlying data with the global config. This is safe because these maps are read-only after startup. Sessions never mutate the global config. See Invariants for details.

### Tool Registration
- Implement `tools.Toolset`, call `r.Register(schema, preparer)` in `Register()`.
- Tool schemas use OpenAI function-calling format (JSON Schema).
- Tool preparers: `func(args []byte) (PreparedTool, error)`. Deserialize and validate once, then capture the validated values in `PreparedTool.Execute`.
- Return a preparation error only for malformed or invalid model-supplied arguments. Return `tools.ErrorResult(err), nil` from `PreparedTool.Execute` for runtime failures the model should react to (missing files, HTTP status failures, size limits, platform send failures, task-manager errors, etc.).
- If a tool returns `ToolResult.Blocks`, `Agent.Run` will send a placeholder `tool` message plus a follow-up multimodal `user` message after every tool result in that batch. Keep `Text` meaningful when possible so text-only models still receive useful context.
- Platform-specific tools: implement `tools.Toolset` on the platform's `Outbound` (the session registry auto-discovers this via type assertion).
- New tools must be safe under serial dispatch. Document any state they share with `tasks.Manager`.

### Event System
- New event types: add struct in `events/events.go`, implement marker methods (`agentEvent()`, `messageEvent()`, `workerEvent()` as appropriate). `TextInputEvent` and `ImageInputEvent` are `AgentEvent`+`MessageEvent`; `/queue` wraps them in `QueuedInputEvent` (a `WorkerEvent` only) for the worker queue.
- Add handling in `scheduler.go:dispatch()` and `worker.go:handleEvent()`.
- Route new `WorkerEvent` types in `scheduler.go:dispatch()` and handle in `worker.go:handleEvent()`.

### Prompt System
- System prompts built from workspace `.md` files. Base sections are `USER.md`, `PERSONA.md`, `RULES.md`, `CONTEXT.md`, `TOOLS.md` (in that order). Heartbeat appends `HEARTBEAT.md`; cron appends `CRON.md`. `SubagentPrompt` only loads `TOOLS.md` plus a caller-supplied system prompt — subagents do not inherit the parent's persona.
- New prompt type: embed `promptBase`, declare your section list — don't copy-paste `writeFile()` calls.

## Architecture Debt

These are known design weaknesses that are not bugs but will increase maintenance cost if left unaddressed. Fix them when you're already in the area; don't schedule standalone refactors unless a feature demands it.

### MEDIUM: Host/Container runtime code duplication

`Spawn` in both runtimes constructs an identical `ProcessHandle` (~15 lines each). `ReadFile` post-processing is identical. The two impls differ only in `buildExecArgs`, file I/O primitives (`os.ReadFile` vs `cat`), and command execution scaffolding. Extract `spawnFromCmd(cmd) (*ProcessHandle, error)` and the shared post-processing logic into `runtime.go`.

### LOW: Inconsistent retry policies across messaging channels

| Channel | Retry count | Backoff | Max wait |
|---------|------------|---------|----------|
| Feishu outbound | 3 | None (immediate) | — |
| WeChat inbound | Infinite | Fixed 5s | — |
| WeChat outbound | 0 | — | — |
| WebSocket outbound | 0 | — | — |
| OpenAI provider | 99 | Exponential | 300s |

The OpenAI 99-attempt cap is excessively high (theoretical maximum of ~8h); in practice the 5xx loop rarely exceeds 10 attempts before context cancellation. Consider capping at 7–10 attempts. The messaging channels should converge on a shared `retryableSend` helper with bounded attempts and exponential backoff.

## Known Issues & Required Fixes

### LOW: Test coverage gaps
Highest-value untested seams:
- `internal/messaging/feishu/inbound.go` — parsing, enqueue failures, parent-message lookup.
- `internal/messaging/wechat/inbound.go` — QR login, session re-login, dedup.
- `internal/messaging/websocket/outbound.go` — writeJSON, send_image.
- `internal/api/server.go` — token gate, WS upgrade, image enqueue.
- `internal/browser/broker.go` — WebSocket broker for the Chrome extension bridge (single-goroutine event loop, auth, request/response correlation).
- `internal/browser/toolset.go` — per-session browser client and toolset; registers `browser_tabs` / `browser_new_tab` / `browser_snapshot` / `browser_click` / `browser_type` / `browser_press_key` / `browser_select_option` / `browser_navigate` / `browser_evaluate` / `browser_wait` tools. Scope-isolated; each session/subagent gets its own scope ID.
- `tools/background.go` (does not exist yet — `CommandToolset` is the replacement) and `internal/tasks/` retention / subagent shutdown.

## Development Rules

### Adding a New Tool
1. Define tool in a `Toolset` implementation.
2. Schema: JSON Schema construction (set `Parallel: true` for tools that can run concurrently with siblings in a single tool-call batch).
3. Handler: always unmarshal args with `json.Unmarshal`, return `(ToolResult, error)`.
4. Register in the appropriate toolset's `Register()` method.
5. For platform-specific tools, implement `tools.Toolset` on the platform's `Outbound`.
6. Confirm it's safe under serial dispatch; document any shared state with `tasks.Manager`.

### Adding a New Messaging Platform
1. Create a subpackage under `internal/messaging/<platform>/`.
2. Implement `events.Outbound` (Send / SendDelta / SendFinal / StartThinking / EndThinking) on a type in that package.
3. Implement `messaging.Inbound` (Run) on an inbound type that pushes events into the shared `inbox.Inbox[events.AgentEvent]`.
4. Optionally implement `tools.Toolset` on the `Outbound` for platform-specific tools (e.g. reactions, uploads).
5. Wire in `cmd/bot/main.go` — keep the integration optional behind a config flag (Feishu uses `cfg.Feishu.AppID != ""`; WeChat uses `cfg.WeChat.Enabled`).

### Adding a New LLM Provider
1. Implement `llm.CompletionClient` (`Complete(ctx, CompletionRequest) (CompletionResponse, error)`).
2. Add config fields in `config.Config`.
3. Add construction branch in `main.go:buildLLMClient()`.
4. Use raw `net/http` — do not introduce a vendor SDK. Reuse the retry discipline in `openai.go:retryExponential`.

### Adding a New Event Type
1. Define struct in `events/events.go` with appropriate marker methods.
2. Handle in `scheduler.go:dispatch()` (routing) and `worker.go:handleEvent()` (processing).
3. Ensure the event carries a `ChatID` field for routing; the scheduler dispatches by `ChatID` directly.
4. If the event carries a Sender and its `process*` function may fail, make it call `ev.Sender.Send(...)` with the error before returning it. The worker loop only logs non-user-visible failures; errors that should reach the user are reported at the source.

### Adding a New Goroutine
1. Update the **Concurrency Map** above. Always.
2. State which channel(s) it owns and which it sends to.
3. Document who stops it (context cancellation, explicit Stop, etc.).

### Testing Guidelines
- Existing tests: `api/server_test.go`, `config/config_test.go`, `engine/agent_test.go`, `engine/agent_edge_test.go`, `engine/cron_test.go`, `engine/loop_test.go`, `engine/orchestrator_test.go`, `engine/session_test.go`, `engine/scheduler_test.go`, `engine/worker_config_test.go`, `inbox/inbox_test.go`, `llm/openai_test.go`, `messaging/dedup/dedup_test.go`, `messaging/feishu/outbound_test.go`, `messaging/wechat/media_test.go`, `runtime/runtime_test.go`, `tasks/manager_test.go`, `tasks/manager_lifecycle_test.go`, `tools/command_test.go`, `tools/format_test.go`, `tools/registry_test.go`.
- Highest-value untested seams: see the "Test coverage gaps" entry in Known Issues above.
- Mock `CompletionClient`, `Runtime`, and `Outbound` interfaces — they're the natural seams.
- When testing `HostRuntime` behavior that depends on filesystem scope, prefer absolute temp-dir paths/patterns over relying on `os.Chdir`; `Execute`/`Spawn` run through a login shell whose profile may change the child process cwd.
- When testing tool-call loops, assert the exact replayed message order sent to the second LLM request: assistant tool calls, all matching `tool` messages, then any multimodal followups, then any in-loop user interrupt.
- Always run `go test ./... -race -count=1` before merging changes that touch concurrency.

### Dependency Injection
- All major components accept interfaces, not concrete types.
- Orchestrators are mode-specific collaborators constructed at worker call sites and wired through the `Orchestrator` interface.
- `Runtime` abstracts host vs container execution.
- Avoid adding direct dependencies between sibling packages under `internal/`. Shared contracts (event types, tool protocol, generic inbox) live in their own small packages (`events`, `tools`, `inbox`) so multiple consumers can depend on them without coupling siblings.

## Build & Run

```bash
# Build
go build -o bot ./cmd/bot

# Run (requires config.yaml)
./bot

# Or specify a config path:
./bot /path/to/config.yaml
```

## File Quick Reference

| When you need to... | Look at... |
|---------------------|------------|
| Add/change config | `internal/config/config.go` |
| Add a tool | `internal/tools/toolbox.go` or new toolset file |
| Add a background-task tool | `internal/tools/command.go` + `internal/tasks/` |
| Add an event type | `internal/events/events.go` |
| Change LLM behavior | `internal/engine/agent.go` (loop), `internal/llm/openai.go` (provider) |
| Change the agent loop / context compression | `internal/engine/agent.go` |
| Change prompt content | `internal/llm/prompt.go` + workspace `.md` files |
| Change message routing | `internal/engine/scheduler.go` |
| Change worker behavior | `internal/engine/worker.go` |
| Add slash command | `internal/engine/scheduler.go:handleSlashCommand()` |
| Add cron features | `internal/engine/cron.go` |
| Add messaging platform | `internal/messaging/<platform>/` (own subpackage) + `cmd/bot/main.go` |
| Sandbox tool execution | `internal/runtime/container.go` |
| Touch the shared event channel | `internal/inbox/inbox.go` (interface) / `Memory[T]` (impl) |
| Add subagent/fleet tool | `internal/engine/subagent.go` |
| Tune LLM HTTP behavior | `internal/llm/openai.go` (retry, streaming, extra_body passthrough) |
| Adjust dedup TTL / capacity | `internal/messaging/dedup/dedup.go` |
| Frontend chat UI | `chat.html` + `internal/api/server.go` |
