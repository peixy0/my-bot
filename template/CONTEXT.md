# CONTEXT.md — Workspace Operations Manual

> Loaded automatically into every system prompt.
> **Update when**: The workspace structure changes or memory conventions evolve.

## Workspace Layout

```
/workspace/
├── PERSONA.md          # Who you are
├── RULES.md            # Hard constraints
├── CONTEXT.md          # This file
├── TOOLS.md            # Tool usage guardrails
├── USER.md             # Human's long-term profile
├── INSIGHTS.md         # Long-term observations
├── TODO.md             # Backlog and objectives
├── journal/            # Daily working memory
│   └── YYYY-MM-DD/
│       ├── notes.md
│       └── [topic].md
├── .skills/            # Discoverable agent skills
├── .cron/              # Scheduled task definitions
└── .session/           # Conversation state persistence
```

## Memory Architecture

### Short-Term: `journal/YYYY-MM-DD/`
Raw observations, debug logs, exploration notes. One file per topic per day. Promote to long-term only when confident.

### Long-Term: `INSIGHTS.md`, `RULES.md`, `PERSONA.md`, `USER.md`
Refined patterns, confirmed rules, stable preferences. Only updated after a pattern is confirmed, not when first observed.

## Auto-Loaded Files

These files are composed into the system prompt automatically on every session:

`PERSONA.md` → `RULES.md` → `CONTEXT.md` → `TOOLS.md` → `USER.md` → (Skills list) → (OS info)

For heartbeat and cron sessions, `HEARTBEAT.md` or `CRON.md` is appended after `USER.md`.
Subagents only receive `TOOLS.md` plus the caller's task description.

`INSIGHTS.md` and `TODO.md` are NOT auto-loaded — read them when relevant to the current task.

## Subagent Delegation

Subagents only receive TOOLS.md plus the caller's task description. They do NOT inherit PERSONA.md or RULES.md. Keep task descriptions self-contained. Validate subagent output before presenting to the human.
