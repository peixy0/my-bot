# my-bot

An event-driven AI agent framework in Go. Connects messaging platforms (Feishu/Lark, WebSocket) to OpenAI-compatible LLMs with an extensible tool system. Supports interactive conversations, autonomous heartbeat cycles, and cron-scheduled tasks.

## Highlights

- **Event-driven core.** A single scheduler dispatches inbound events to per-chat workers via channels. State is owned by single goroutines wherever possible — locks are a last resort.
- **OpenAI-compatible LLM provider.** Chat completions are streamed via raw `net/http` (no vendor SDK).
- **Composable tools.** `DefaultToolset` covers `read_file` / `read_file_range` / `write_file` / `append_file` / `edit_file` / `grep` / `glob` / `web_search` / `fetch` / `read_image` (when vision is enabled) / `use_skill`; `read_file` is line-oriented for ordinary source, while `read_file_range` reads byte windows for extremely long lines; `CommandToolset` covers long-running processes (`run_command` / `get_task` / `await_task` / `list_tasks` / `kill_task` / `write_to_task`); the `agent` / `fleet` subagent toolsets delegate isolated work; platform adapters (`feishu`, `websocket`) add their own `send_image` / `send_file` / `add_reaction` tools. Add new tools by implementing a small `Toolset` interface.
- **Composable prompts.** System prompts assemble from workspace `.md` files (`USER.md`, `PERSONA.md`, `RULES.md`, `CONTEXT.md`, `TOOLS.md`, plus per-mode files `HEARTBEAT.md` / `CRON.md`). The `SubagentPrompt` is deliberately lean — it loads only `TOOLS.md` plus a caller-supplied block.
- **Three orchestration modes.** Interactive (with live user interrupts), background (heartbeat / cron), and isolated subagents.
- **Runtime abstraction.** Tools execute on host bash or in a podman/docker sandbox with no caller changes.
- **Skills system.** Markdown files with frontmatter become discoverable, on-demand-loadable agent skills.

## Quick Start

```bash
# Build
(cd support/webui && npm ci && npm run build)
go build -o bot ./cmd/bot

# Write your config (see "config.example.yaml" for the full schema)
cat > config.yaml <<'EOF'
log_level: debug
llm:
  api_key: sk-...        # your OpenAI-compatible provider's API key
  base_url: https://api.openai.com/v1
  model: gpt-4o
feishu:                   # Feishu is optional; omit if you don't need it
  app_id: ...
  app_secret: ...
wechat:                   # WeChat iLink bot is optional; defaults to disabled
  enabled: false
webui:                    # WebUI defaults to enabled (host 127.0.0.1, port 8017)
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
  temperature: 1.0      # optional
  # top_p: 0.95         # optional; omitted from API requests when unset
  # top_k: 40           # optional; omitted from API requests when unset
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
  token: change-me                    # optional; gates ws://host:port/api/bot?token=...
  assets: ../support/webui/dist       # Vite build output, relative to workspace.cwd
```

Build the WebUI with `npm ci && npm run build` in `support/webui` before starting the bot. The generated `dist` directory is not committed.

Run the frontend quality checks with `npm run check`. ESLint uses type-aware TypeScript, React Hooks, React Refresh, and JSX accessibility rules; `npm run format` applies Prettier.

Other notable sections (defaults shown):

```yaml
tool:
  max_output_chars: 100000     # cap on text payloads returned to the model
  web_search_api: ""           # enables the web_search tool; empty disables it
  fetch_proxy: ""              # optional proxy for the fetch tool

workspace:
  cwd: ./workspace             # where PERSONA.md / RULES.md / etc. live
  skills_dir: ./.skills
  crons_dir: ./.cron
  session_dir: ./.session

context:
  max_image_bytes: 5242880       # 5 MiB
  max_output_tokens: 16384
  compression_threshold: 0.7   # fraction of context_window that triggers compression

llm:
  temperature: 1.0
  # top_p: 0.95                # optional; omitted from API requests when unset
  # top_k: 40                  # optional, non-OpenAI providers
  context_window: 128000
  vision: false
  # extra_body:                # passthrough for provider-specific knobs
  #   chat_template_kwargs:
  #     enable_thinking: true

presets:                       # named presets selectable by session or slash command
  qwen-completion:
    model: Qwen3-32B
    temperature: 0.6
    context_window: 131072
    vision: true
    extra_body: {chat_template_kwargs: {enable_thinking: true}}

  qwen-compression:
    model: Qwen3-8B
    temperature: 0.2

context:
  compression_model: qwen-compression # preset name or a direct provider model name

heartbeat:
  interval_seconds: 1800

container:                     # tool-execution sandbox
  enabled: false
  runtime: podman              # podman | docker
  name: my-bot-sandbox

sessions:                      # per-chat overrides (keyed by chat id)
  "oc_xxx":
    model: qwen-completion          # preset name or a direct provider model name
    compression_model: gpt-4o-mini  # optional; otherwise uses the current model settings

wechat:                        # WeChat iLink bot (QR login if bot_token is empty)
  enabled: false
  # bot_token: ""              # optional: skip QR login on startup
  # base_url: ""               # optional: override https://ilinkai.weixin.qq.com
```

## Slash Commands

While chatting with the bot:

| Command | Effect |
|---|---|
| `/new` | Start a fresh session in this chat |
| `/drop` | Drop the current session and worker |
| `/heartbeat [seconds]` | Begin autonomous heartbeat cycles in this chat, optionally overriding `heartbeat.interval_seconds` |
| `/model <name>` | Switch the active LLM model for the current chat/session (applies any matching model preset) |
| `/vision on|off` | Toggle image input support for the current chat/session |
| `/temperature <0..2>` | Set sampling temperature for the current chat/session |
| `/max_tokens <positive int>` | Set max output tokens for the current chat/session |
| `/context_window <positive int>` | Set context window size (compression trigger) for the current chat/session |
| `/queue <text>` | Queue a message to run after the current agent loop finishes |
| `/dump` | Save the current conversation to a UUID-named session file |
| `/resume <id>` | Load a saved conversation into the current chat/session |
| `/session` | Echo the current chat id |
| `/abort` | Abort the currently running completion (no-op if none) |
| `/cron load <name>` | Reload cron tasks defined in `.cron/<name>/*.md` |
| `/cron unload <name>` | Stop a loaded cron job |
| `/cron trigger <name>` | Fire a cron job immediately without waiting for its schedule |
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
Inbound (Feishu / WebSocket / WeChat / cron / heartbeat / subagent)
  → AgentEvent → shared inbox (chan)
    → Scheduler.dispatch  (single goroutine)
      → ConversationWorker  (per chat, own goroutine)
        → Orchestrator + Agent.Run()  (LLM ↔ tools)
          → Outbound.Send / SendDelta / SendFinal  (reply to user)
```

Key directories:

- `cmd/bot` — entry point and dependency wiring.
- `internal/config` — YAML configuration (loaded once at startup); supports per-session overrides and named model presets.
- `internal/events` — event types and the `Outbound` interface.
- `internal/inbox` — generic `Inbox[T]` channel-backed pub/sub used by every event queue.
- `internal/engine` — scheduler, per-chat workers, cron loader.
- `internal/llm` — agent loop, orchestrators, prompt builders, subagent/fleet toolsets, OpenAI-compatible provider.
- `internal/messaging` — Feishu, WebSocket, and WeChat adapters, plus a shared `dedup` primitive.
- `internal/runtime` — host-bash and container execution backends.
- `internal/tools` — tool protocol, registry, built-in toolsets, skill loader.
- `internal/tasks` — event-driven task manager, drivers, retention, output buffer.
- `internal/util` — JSON encoding helpers used across the codebase.
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
