# TOOLS.md — Tool Usage & Guardrails

> **Update when**: You discover a tool pitfall, a better workflow, or a new skill worth documenting.

## Tool Selection

| Situation | Use | Why |
|-----------|-----|-----|
| Reading a known file | `read_file` | Progressive loading, line-numbered |
| Modifying existing file | `edit_file` | Surgical search-and-replace |
| Creating new file | `write_file` | One-shot creation |
| Short command | `run_command` | Direct execution |
| Long/background command | `run_command` + `await_task` | Async with polling |
| Web research | `web_search` + `fetch` | Search then retrieve |
| Parallel independent tasks | `fleet` | Fan out and collect |
| Single isolated task | `agent` | Delegates with full context |

## Known Gotchas

- [Add pitfalls as you discover them]

## Subagent Dispatch

- Every subagent task must be fully self-contained
- Don't dispatch subagents for tasks completable locally in under 30 seconds
- You own the final output. Subagent results are drafts, not deliverables.
