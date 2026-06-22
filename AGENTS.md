# AGENT.md — Development Guide for Coding Agents

This document guides AI coding agents working on this project. It covers the design philosophy, architecture, conventions, intentional choices, known issues, and development rules.

## Project Overview

An event-driven AI agent framework in Go. It connects messaging platforms (Feishu/Lark, WebSocket) to OpenAI-compatible LLMs with an extensible tool system. Supports autonomous operation via heartbeats and cron scheduling.

**Module:** `my-bot`
**Go version:** 1.26
**Entry point:** `cmd/bot/main.go`

## Design Philosophy

These are the core principles we hold the codebase to. They are load-bearing — please don't drift from them without good reason.

- **Event-driven over shared state.** Components communicate via channels and events, not shared mutable state. The Scheduler's single-goroutine `dispatch()` is the canonical pattern: ownership of `sessions` belongs to one goroutine, no lock needed. Prefer this model when adding new components.
- **Locks are a last resort.** A `sync.Mutex` is an admission that we couldn't structure the code as a single owner. Before adding one, ask: can this state live behind a channel, in a single goroutine, or be made immutable? Locks are acceptable only at boundaries where external libraries hand us callbacks on their own goroutines.
- **Interface-driven boundaries for testability.** Every cross-package collaborator is an interface (`CompletionClient`, `Outbound`, `Runtime`, `Toolset`). Don't add concrete-to-concrete dependencies between sibling packages under `internal/`.
- **Composable prompts and tools.** Prompts compose workspace `.md` files via `promptBase`; tools register into a `Registry`. Extending the system means adding implementations, not modifying the core.
- **Fail loudly at boundaries, gracefully inside.** Errors at I/O boundaries (Feishu HTTP, file reads, LLM calls) get logged at `slog.Warn` or higher. Internal invariant violations should be unreachable, not silently swallowed.

## Architecture

```
cmd/bot/main.go          ← entry point, wiring
internal/
  config/config.go       ← YAML-based configuration (30+ fields)
  events/events.go       ← event types & Outbound interface
  engine/
    scheduler.go         ← event dispatcher, session lifecycle, slash commands
    session.go           ← per-chat session owner for tools, cron, and shutdown
    worker.go            ← per-chat conversation logic and event loop
    cron.go              ← cron job loading & scheduling
  llm/
    agent.go             ← LLM agent loop + context compression
    loop.go              ← AgentLoop (owns Conversation only)
    orchestrator.go      ← tool dispatch and response strategies
    subagent.go          ← subagent/fleet task startup service
    reg.go               ← registry builders for session and subagent execution
    prompt.go            ← composable system prompt builders
    openai.go            ← OpenAI-compatible provider (streaming raw net/http, no SDK)
  messaging/
    inbound.go           ← Inbound interface
    websocket.go         ← WebSocket outbound
    feishu/              ← Feishu/Lark adapter (own subpackage)
      feishu.go          ← Config, single-owner dedup window
      inbound.go         ← webhook handler
      outbound.go        ← events.Outbound impl
      tools.go           ← reactions, image/file upload tools
  runtime/
    runtime.go           ← Runtime interface (Execute, Spawn, ReadFile, etc.)
    host.go              ← local bash execution
    container.go         ← podman/docker execution
  toolkit/
    types.go             ← ToolSchema, ToolResult, ToolHandler protocol types + ToolRegistrar, ToolRegistry interfaces
  tools/
    registry.go          ← Registry (implements ToolRegistry), Toolset interface
    toolbox.go           ← DefaultToolset (8+ core tools)
    command.go           ← CommandToolset task APIs (run_command, await_task, etc.)
    skill.go             ← SkillLoader (frontmatter .md files)
    search.go            ← web search
    fetch.go             ← HTTP fetch
    format.go            ← formatting utilities
  tasks/                 ← event-driven TaskManager, task drivers, retention
  api/
    server.go            ← HTTP + WebSocket server
```

### Data Flow

```
Inbound (Feishu/WS) → AgentEvent → queue (chan) → Scheduler.dispatch()
  → ConversationWorker.Events (per-chat channel)
    → Orchestrator + Agent.Run() loop (LLM ↔ tools)
      → Outbound.Send() (reply to user)
```

### Concurrency Map

Every long-lived goroutine and what it owns. This is the single source of truth — when you add a goroutine, update this table.

| Goroutine | Started by | Owns / Touches | Synchronization |
|---|---|---|---|
| `Scheduler.Run` | main | `sessions` map; reads `queue` | none — single owner |
| `chatSession.run` (per chat) | Scheduler | session shutdown path, worker lifetime | none within session |
| `ConversationWorker.Run` (per chat) | chatSession | conversation state, `Events` chan, in-loop input chan | none within worker |
| `cron.Cron` internal goroutines | CronWorker (via `cron.Cron.Start`) | invokes registered funcs that send on `workerCh` | callbacks only touch a channel; no shared map access |
| Feishu image download | Feishu inbound | downloads bytes, enqueues an event, surfaces failures to user | fire-and-forget; logs every error path |
| Heartbeat `time.AfterFunc` | ConversationWorker | non-blocking send to worker `Events` | timer is `Stop()`-ed on shutdown |
| `tasks.Manager` loop | chatSession or subagent runner | task table, state transitions, retention | single owner goroutine + request channel |
| Process task pumps | `tasks.NewProcessDriver` | per-task stdin/stdout/stderr bridging and exit reporting | per-task goroutines reporting into `tasks.Manager` |
| Feishu dedup | Feishu inbound | `expires` map + `order` slice | single-owner goroutine, channel-fed (see `messaging/feishu/feishu.go`) |

### Key Interfaces

| Interface | Package | Purpose |
|-----------|---------|---------|
| `CompletionClient` | `llm` | LLM provider contract (`Complete()`) |
| `Orchestrator` | `llm` | Controls tool dispatch, message delivery |
| `SystemPrompt` | `llm` | Composable prompt builder (`Build() string`) |
| `Runtime` | `runtime` | Execution environment abstraction |
| `Outbound` | `events` | Send messages back to user |
| `Toolset` | `tools` | Registers tools into a Registry |
| `ToolRegistrar` | `toolkit` | Optional interface for Outbound impls that add platform-specific tools |

### Orchestrator Variants

- **HumanInputOrchestrator** — Interactive conversation; sends partial content, handles live user interrupts.
- **BackgroundOrchestrator** — Heartbeat/cron; suppresses output ending with `NO_REPORT`.
- **SubagentOrchestrator** — Isolated execution; captures final output instead of sending.

## Intentional Design Choices

These are non-obvious decisions worth preserving. If you're tempted to "fix" one of these, read the rationale first.

- **Single-goroutine Scheduler dispatch.** `Scheduler.sessions` is deliberately unprotected by mutex. The invariant: only `Run()` reads/writes it. If you ever need access from elsewhere, send an event into the queue rather than introducing a lock. The map is documented in `scheduler.go`.
- **Per-chat ConversationWorker isolation.** Each chat has its own goroutine, its own `Events` channel, and its own conversation state. No cross-chat sharing means no cross-chat synchronization.
- **Session owns resources; worker borrows them.** `chatSession` owns `tasks.Manager`, `CommandToolset`, cron lifecycle, and shutdown. `ConversationWorker` uses these resources through `sessionTools` and should not become an owner again.
- **Per-session config isolation.** `Scheduler` calls `cfg.ForSession(chatID)` to produce a per-session Config. Each `chatSession` and its single `ConversationWorker` share this copy; `/model` and `/vision` changes affect only that session. Sessions never share Config instances.
- **Live user input uses a separate in-loop input inbox, not the worker queue.** While an agent loop is running, ordinary text/images are routed as non-blocking interrupts with a `default` fallthrough; `/queue` wraps the input in `QueuedInputEvent` and dispatches it as a `WorkerEvent` for handling after the current loop finishes.
- **Three orchestrator variants instead of mode flags.** Strategy pattern keeps each orchestrator's responsibility crisp; we'd rather grow a fourth variant than feature-flag an existing one.
- **Raw `net/http` for LLM calls, no SDK.** The OpenAI-compatible provider serializes messages and tools as `map[string]any` and consumes streaming chat-completion events directly. Don't reintroduce `openai-go`.
- **Composable system prompts.** Prompts compose workspace `.md` files; behavior is data-driven, not code-driven. New prompts should declare their section list, not duplicate file-reading logic.
- **`Runtime` abstraction.** Lets the same tools execute on host bash or in a container with no caller changes.
- **Task-first asynchronous execution.** Commands, delegated agents, and fleets all create `Task` objects and return `task_id` immediately. Progress, output, and termination flow through `internal/tasks`.
- **Event-driven task ownership.** `tasks.Manager` is the single owner of task state, using a request/event channel rather than mutexes. The public task model is flat: commands, agents, and fleets all surface as independent tasks identified by `task_id`.
- **Tool dispatch is selectively parallel.** `runDispatch()` runs tools in parallel only when their schema marks them `Parallel`; non-parallel tools are still serialized through a mutex in the dispatcher.

## Conventions

### Code Style
- Standard Go style. No external linter config — follow `gofmt`.
- `internal/` for all non-main packages. Nothing is exported outside the module.
- Error wrapping with `fmt.Errorf("context: %w", err)` — use sparingly, only at package boundaries.
- `slog` for structured logging. `Error` for failures, `Warn` for degraded paths, `Info` for lifecycle events.

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

- `Scheduler.sessions` is accessed only from the `Run()` goroutine (via `dispatch` / `handleSlashCommand` / `handleCronCommand`, all called inline). No mutex. If you ever need cross-goroutine access, route it through the event queue.
- `CronWorker.jobs` is mutated only from the Scheduler goroutine. The `cron.Cron` library invokes registered funcs on its own goroutines, but those funcs only send on `workerCh` — they never touch the `jobs` map. No mutex.
- `messaging.dedup` is the canonical example of the channel-as-single-owner pattern: one goroutine owns the `expires` map and `order` slice; all callers reach it via the request channel `dedup.in`. The goroutine dies cleanly when the inbound context is cancelled. If `dedup.check` cannot reach the goroutine within 1s, it fails open (returns true) — under shutdown we'd rather risk a duplicate than a deadlock.
- `ConversationWorker.heartbeatTimer` is a `time.AfterFunc` that may outlive normal event processing. It is `Stop()`-ed at the top of every event-loop iteration. Session teardown cancels the worker context, which ends the loop and triggers resource shutdown in `chatSession`.
- `ConversationWorker.cfg` is a per-worker `Config` clone; model and vision changes affect only that worker. The base `Agent` is shared and never forked — `http.Client` is thread-safe.
- `chatSession` owns `sessionTools`, `CronWorker`, and the only shutdown path for `tasks.Manager`. `ConversationWorker` must not call `tasks.Manager.Shutdown()` directly.
- The Feishu image-download goroutine is fire-and-forget but every error path logs at `Warn` and surfaces a single user-visible failure message via `outbound.Send`. Don't add new fire-and-forget paths without the same discipline.

### Configuration
- All config via a single YAML file (default `./config.yaml`), loaded once in `config.Load()`.
- Defaults in `config.go`. New settings: add field to `Config` struct with `yaml` tag, add default in `defaultConfig()`, add validation in `Validate()`.

### Tool Registration
- Implement `tools.Toolset`, call `r.Register(schema, handler)` in `Register()`.
- Tool schemas use OpenAI function-calling format (JSON Schema).
- Tool handlers: `func(ctx context.Context, args []byte) (ToolResult, error)`.
- Platform-specific tools: implement `toolkit.ToolRegistrar` on your `Outbound`.
- New tools must be safe for the current serial-dispatch model. Document any state they share with `tasks.Manager`.

### Event System
- New event types: add struct in `events/events.go`, implement marker methods (`agentEvent()`, `messageEvent()`, `workerEvent()` as appropriate). `TextInputEvent` and `ImageInputEvent` are `AgentEvent`+`MessageEvent`; `/queue` wraps them in `QueuedInputEvent` for the worker queue.
- Add handling in `scheduler.go:dispatch()` and `worker.go:handleEvent()`.
- Route new `WorkerEvent` types in `scheduler.go:dispatch()` and handle in `worker.go:handleEvent()`.

### Prompt System
- System prompts built from workspace `.md` files (PERSONA.md, RULES.md, CONTEXT.md, TOOLS.md, plus per-mode files).
- New prompt type: embed `promptBase`, declare your section list — don't copy-paste `writeFile()` calls.

## Known Issues & Required Fixes

### CRITICAL: SRP violation in FeishuOutbound

**Resolved.** The Feishu adapter has been split into its own subpackage `internal/messaging/feishu/` (`feishu.go` for config + dedup, `inbound.go` for webhooks, `outbound.go` for the `events.Outbound` impl, `tools.go` for `add_reaction` / `send_image` / `send_file`). Adding new platforms should follow the same pattern: one subpackage under `internal/messaging/`, with `inbound.go` / `outbound.go` / `tools.go` as needed.

### HIGH: Feishu dedup is single-owner channel-based

**Resolved.** `internal/messaging/feishu/feishu.go` implements the `dedup` window as a single-owner goroutine fed by a request channel — no mutex. 5-minute TTL eviction plus a 1024-entry capacity backstop. Use this as the reference pattern for any future "shared map across goroutines" temptation.

### MEDIUM: Swallowed errors in I/O paths

Continue propagating `io.ReadAll` and streaming parse errors at I/O boundaries. `Sender.Send()` failures are logged at `Warn` level; for `json.Marshal` of known-good schemas, ignoring is acceptable.

## Development Rules

### Adding a New Tool
1. Define tool in a `Toolset` implementation.
2. Schema: JSON Schema construction.
3. Handler: always unmarshal args with `json.Unmarshal`, return `(ToolResult, error)`.
4. Register in the appropriate toolset's `Register()` method.
5. For platform-specific tools, implement `toolkit.ToolRegistrar` on the `Outbound`.
6. Confirm it's safe under serial dispatch; document any shared state with `tasks.Manager`.

### Adding a New Messaging Platform
1. Create a subpackage under `internal/messaging/<platform>/`.
2. Implement `events.Outbound` (Send, StartThinking, EndThinking) on a type in that package.
3. Implement `messaging.Inbound` (Run) on an inbound type that pushes events into the queue.
4. Optionally implement `toolkit.ToolRegistrar` for platform-specific tools (e.g. reactions, uploads).
5. Wire in `cmd/bot/main.go` — keep the integration optional behind a config flag, as Feishu is.

### Adding a New LLM Provider
1. Implement `llm.CompletionClient` interface.
2. Add config fields in `config.Config`.
3. Add construction branch in `main.go:buildLLMClient()`.
4. Use raw `net/http` — do not introduce a vendor SDK.

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
- Existing tests cover `api/server_test.go`, `config/config_test.go`, `engine/cron_test.go`, `llm/agent_test.go`, `llm/openai_test.go`, `llm/orchestrator_test.go`, `messaging/feishu/dedup_test.go`, and `tools/background_test.go`. More are needed.
- Highest-value untested seams:
  - `engine/scheduler.go` — dispatch routing, slash-command handling.
  - `internal/messaging/feishu/inbound.go` and `outbound.go` — parsing, enqueue failures, and send retry behavior.
  - `tools/background.go` + `internal/tasks/` — task lifecycle, retention, and session/subagent shutdown.
  - `engine/cron.go` — job loading, invalid-expr handling.
- Mock `CompletionClient`, `Runtime`, and `Outbound` interfaces — they're the natural seams.
- Always run `go test ./... -race -count=1` before merging changes that touch concurrency.

### Dependency Injection
- All major components accept interfaces, not concrete types.
- Orchestrators are mode-specific collaborators constructed at worker call sites and wired through the `Orchestrator` interface.
- `Runtime` abstracts host vs container execution.
- Avoid adding direct dependencies between sibling packages under `internal/`. The `toolkit` package holds shared protocol types (`ToolSchema`, `ToolHandler`, `ToolResult`) and interfaces (`ToolRegistrar`, `ToolRegistry`) that `tools`, `messaging`, and `llm` all depend on. Sibling packages must not import each other directly; shared contracts go in `toolkit`.

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
| Change LLM behavior | `internal/llm/agent.go` (loop), `openai.go` (provider) |
| Change the agent loop / context compression | `internal/llm/loop.go`, `internal/llm/agent.go` |
| Change prompt content | `internal/llm/prompt.go` + workspace `.md` files |
| Change message routing | `internal/engine/scheduler.go` |
| Change worker behavior | `internal/engine/worker.go` |
| Add slash command | `internal/engine/scheduler.go:handleSlashCommand()` |
| Add cron features | `internal/engine/cron.go` |
| Add messaging platform | `internal/messaging/<platform>/` (own subpackage) + `cmd/bot/main.go` |
| Sandbox tool execution | `internal/runtime/container.go` |
