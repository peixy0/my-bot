# my-bot

An event-driven AI agent framework in Go. Connects messaging platforms (Feishu/Lark, WebSocket) to OpenAI-compatible LLMs with an extensible tool system. Supports interactive conversations, autonomous heartbeat cycles, and cron-scheduled tasks.

## Highlights

- **Event-driven core.** A single scheduler dispatches inbound events to per-chat workers via channels. State is owned by single goroutines wherever possible — locks are a last resort.
- **OpenAI-compatible LLM provider.** Chat completions are streamed via raw `net/http` (no vendor SDK).
- **Composable tools.** Built-in tools for filesystem, shell, web search, fetch, edit, glob, grep, and long-running background processes. Add new tools by implementing a small `Toolset` interface.
- **Composable prompts.** System prompts assemble from workspace `.md` files (`PERSONA.md`, `RULES.md`, `CONTEXT.md`, `TOOLS.md`, plus per-mode files).
- **Three orchestration modes.** Interactive (with live user interrupts), background (heartbeat / cron), and isolated subagents.
- **Runtime abstraction.** Tools execute on host bash or in a podman/docker sandbox with no caller changes.
- **Skills system.** Markdown files with frontmatter become discoverable, on-demand-loadable agent skills.

## Quick Start

```bash
# Build
go build -o bot ./cmd/bot

# Configure (minimal, OpenAI only)
export OPENAI_API_KEY=sk-...
# Optional: a workspace where the agent reads PERSONA.md / RULES.md / etc.
export CWD=$(pwd)/workspace

# Run
./bot
```

To enable Feishu/Lark:

```bash
export FEISHU_APP_ID=...
export FEISHU_APP_SECRET=...
export FEISHU_ENCRYPT_KEY=...
export FEISHU_VERIFICATION_TOKEN=...
```

To enable the local WebUI:

```bash
export WEBUI_ENABLED=true
export WEBUI_HOST=127.0.0.1
export WEBUI_PORT=8017
# Optional: require http://localhost:8017/?token=... and ws://localhost:8017/api/bot?token=...
export WEBUI_TOKEN=change-me
```

Then open `http://localhost:8017`.

## Slash Commands

While chatting with the bot:

| Command | Effect |
|---|---|
| `/new` | Start a fresh session in this chat |
| `/drop` | Drop the current session and worker |
| `/heartbeat [seconds]` | Begin autonomous heartbeat cycles in this chat, optionally overriding `WAKE_INTERVAL_SECONDS` |
| `/model <name>` | Switch the active LLM model for the current chat/session |
| `/vision on|off` | Toggle image input support for the current chat/session |
| `/queue <text>` | Queue a message to run after the current agent loop finishes |
| `/dump` | Save the current conversation to a UUID-named session file |
| `/resume <id>` | Load a saved conversation into the current chat/session |
| `/cron load <name>` | Reload cron tasks defined in `.cron/<name>/*.md` |
| `/cron unload <name>` | Stop a loaded cron job |
| `/cron ls` | List available and loaded cron jobs |

## Cron Jobs

Drop markdown files into `.cron/<job-name>/`:

```markdown
---
name: morning-summary
cron: "0 9 * * *"
---
Generate a morning summary of yesterday's work and send it to me.
```

Then `/cron load morning-summary` schedules it, replacing any previous schedule for that job. The cron expression follows standard 5-field cron (minute, hour, day-of-month, month, day-of-week).

## Skills

Drop markdown files into the skills directory (default `./.skills/`):

```markdown
---
name: review-pr
description: Reviews a GitHub pull request for safety and clarity.
---
You are reviewing a pull request. Steps:
1. Fetch the diff with `gh pr view <num> --json ...`
...
```

The agent discovers skills automatically and loads them on demand via the `use_skill` tool.

## Container Workspace

`Containerfile`, `build-container.sh`, and `run-container.sh` define a workspace sandbox for tool execution. They are not a production image for the Go bot binary; build the bot itself with `go build -o bot ./cmd/bot`.

## Architecture

```
Inbound (Feishu / WebSocket / cron / heartbeat)
  → AgentEvent → queue (chan)
    → Scheduler.dispatch  (single goroutine)
      → ConversationWorker  (per chat, own goroutine)
        → Orchestrator + Agent.Run()  (LLM ↔ tools)
          → Outbound.Send / SendDelta  (reply to user)
```

Key directories:

- `cmd/bot` — entry point and dependency wiring.
- `internal/config` — env-driven configuration.
- `internal/events` — event types and the `Outbound` interface.
- `internal/engine` — scheduler, per-chat workers, cron loader.
- `internal/llm` — agent loop, orchestrators, prompt builders, OpenAI-compatible provider.
- `internal/messaging` — Feishu and WebSocket adapters.
- `internal/runtime` — host-bash and container execution backends.
- `internal/tools` — tool registry and built-in toolsets.
- `internal/api` — HTTP server and WebSocket WebUI.

For design philosophy, intentional choices, the concurrency map, and contributor rules, see [AGENTS.md](./AGENTS.md).

## Development

```bash
# Build, vet, and test (always with -race for concurrency-touching changes)
go build ./...
go vet ./...
go test ./... -race -count=1
```

Read [AGENTS.md](./AGENTS.md) before contributing — it covers the no-comments policy, single-owner invariants, how to add tools / events / providers, and the testing seams.

## License

See repository for license information.
