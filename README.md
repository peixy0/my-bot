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

# Write your config (see "config.example.yaml" for the full schema)
cat > config.yaml <<'EOF'
log_level: debug
llm:
  api_key: sk-...        # your OpenAI-compatible provider's API key
  base_url: https://api.openai.com/v1
  model: gpt-4o
feishu:
  app_id: ...
  app_secret: ...
webui:
  enabled: true
  port: 8017
EOF

# Run (defaults to ./config.yaml, or pass a path)
./bot
```

## Configuration

All configuration lives in a single YAML file (default `./config.yaml`, or pass a path as the first arg to the bot binary). There are no environment variables — everything is in the config.

Minimal OpenAI-only config:

```yaml
log_level: debug         # debug | info | warn | error
llm:
  base_url: https://api.openai.com/v1
  api_key: sk-...
  model: gpt-4o
  temperature: 1.0       # optional
  top_p: 1.0             # optional
```

Enable Feishu/Lark:

```yaml
feishu:
  app_id: ...
  app_secret: ...
  encrypt_key: ...         # required by Feishu's event signature scheme
  verification_token: ...  # required by Feishu's event signature scheme
```

Enable the local WebUI (HTTP + WebSocket):

```yaml
webui:
  enabled: true
  host: 127.0.0.1
  port: 8017
  token: change-me        # optional; gates http://host:port/?token=... and ws://...?token=...
```

Other notable sections:

```yaml
tool:
  max_output_chars: 50000    # cap on text payloads returned to the model
  web_search_api: https://... # enables the web_search tool; empty disables it
  fetch_proxy: http://...     # optional proxy for the fetch tool

workspace:
  cwd: ./workspace              # where PERSONA.md / RULES.md / etc. live
  project_dir: ./dev            # default cwd for read/write/edit tools
  skills_dir: ./.skills
  crons_dir: ./.cron
  session_dir: ./.sessions

context:
  auto_compression: true
  window_tokens: 200000
  max_output_tokens: 16384

heartbeat:
  interval_seconds: 1800

vision:
  enabled: false
  max_image_bytes: 5242880      # 5 MiB

container:                       # tool-execution sandbox
  enabled: false
  runtime: podman                # podman | docker
  name: my-bot-sandbox

sessions:                        # per-chat overrides (keyed by chat id)
  "oc_xxx":
    llm:
      model: gpt-4o-mini
```

## Slash Commands

While chatting with the bot:

| Command | Effect |
|---|---|
| `/new` | Start a fresh session in this chat |
| `/drop` | Drop the current session and worker |
| `/heartbeat [seconds]` | Begin autonomous heartbeat cycles in this chat, optionally overriding `heartbeat.interval_seconds` |
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
- `internal/config` — YAML configuration (loaded once at startup).
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

