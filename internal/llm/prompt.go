package llm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"my-bot/internal/runtime"
	"my-bot/internal/tools"
)

type SystemPrompt interface {
	Build(ctx context.Context) string
}

type promptBase struct {
	skills *tools.SkillLoader
	rt     runtime.Runtime
}

func (b *promptBase) writeFile(sb *strings.Builder, name string) {
	data, err := os.ReadFile(name)
	if err != nil {
		return
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return
	}
	sb.WriteString(content)
	sb.WriteString("\n\n")
}

func (b *promptBase) writeSkills(sb *strings.Builder) {
	summaries := b.skills.Discover()
	if len(summaries) == 0 {
		return
	}
	sb.WriteString("## Available Skills\n\n")
	for _, s := range summaries {
		fmt.Fprintf(sb, "- **%s**: %s\n", s.Name, s.Description)
	}
	sb.WriteString("\nUse the `use_skill` tool to load skill instructions.\n\n")
}

func (b *promptBase) writeOSInfo(ctx context.Context, sb *strings.Builder) {
	info, err := b.rt.OSInfo(ctx)
	if err != nil {
		slog.Debug("osinfo failed", "err", err)
		return
	}
	sb.WriteString(info)
	sb.WriteString("\n")
}

func (b *promptBase) compose(ctx context.Context, sections ...string) string {
	var sb strings.Builder
	for _, s := range sections {
		b.writeFile(&sb, s)
	}
	b.writeSkills(&sb)
	b.writeOSInfo(ctx, &sb)
	return sb.String()
}

func baseSections() []string {
	return []string{"PERSONA.md", "RULES.md", "CONTEXT.md", "TOOLS.md", "USER.md"}
}

type MainPrompt struct{ promptBase }

func NewMainPrompt(skills *tools.SkillLoader, rt runtime.Runtime) *MainPrompt {
	return &MainPrompt{promptBase{skills, rt}}
}

func (p *MainPrompt) Build(ctx context.Context) string {
	return p.compose(ctx, baseSections()...)
}

type HeartbeatPrompt struct{ promptBase }

func NewHeartbeatPrompt(skills *tools.SkillLoader, rt runtime.Runtime) *HeartbeatPrompt {
	return &HeartbeatPrompt{promptBase{skills, rt}}
}

func (p *HeartbeatPrompt) Build(ctx context.Context) string {
	return p.compose(ctx, append(baseSections(), "HEARTBEAT.md")...)
}

type CronPrompt struct{ promptBase }

func NewCronPrompt(skills *tools.SkillLoader, rt runtime.Runtime) *CronPrompt {
	return &CronPrompt{promptBase{skills, rt}}
}

func (p *CronPrompt) Build(ctx context.Context) string {
	return p.compose(ctx, append(baseSections(), "CRON.md")...)
}

type SubagentPrompt struct {
	promptBase
	extra string
}

func NewSubagentPrompt(skills *tools.SkillLoader, rt runtime.Runtime, extra string) *SubagentPrompt {
	return &SubagentPrompt{promptBase: promptBase{skills, rt}, extra: extra}
}

func (p *SubagentPrompt) Build(ctx context.Context) string {
	var sb strings.Builder
	if p.extra != "" {
		sb.WriteString(p.extra)
		sb.WriteString("\n\n")
	}
	p.writeFile(&sb, "TOOLS.md")
	p.writeOSInfo(ctx, &sb)
	return sb.String()
}

const CompressionInstruction = `
You are compressing conversation history into a persistent state anchor for an autonomous AI agent.

The input contains:
- [USER]: user messages.
- [CONTEXT ANCHOR]: an optional previously compressed state.
- [ASSISTANT]: assistant messages.
- [TOOL tool_call(args)]: tool calls and tool results, which may be truncated or omitted.

Your output becomes the ONLY persistent context available to the agent besides the newest message group.

Your task is not to summarize the conversation.
Your task is to reconstruct the smallest complete operational state required for the agent to continue working correctly.

---

## Source of Truth

- Treat [CONTEXT ANCHOR] as previously compressed state, not absolute truth.
- Merge newer messages into the existing state.
- Newer confirmed information overrides older conflicting information.
- Remove stale assumptions when later evidence invalidates them.
- Never invent facts, files, commands, results, decisions, or completion status.
- If something is uncertain, preserve the uncertainty explicitly.

---

## Evidence Handling

- User statements define requirements, constraints, preferences, and requested outcomes.
- Assistant statements define proposed actions, explanations, and reported outcomes.
- Tool results define observed facts only.
- Tool results may be truncated; never assume missing output.
- Preserve diagnostic evidence from failed attempts when it may prevent repeating mistakes.

---

## Preserve Exactly

Always preserve exact values for:

- file paths
- URLs
- identifiers
- IDs
- variable names
- API names
- package names
- package versions
- commands
- command arguments
- environment variables
- configuration values
- error messages
- stack traces
- database/schema names
- numeric values
- user-provided requirements

Do not paraphrase values that must be copied literally.

---

## Task Lifecycle

- Move tasks from Active Tasks to Completed Tasks only when completion is explicitly confirmed.
- Remove fully resolved issues.
- Keep unresolved blockers and unanswered questions.
- Preserve failed approaches only when they contain useful diagnostic information.
- Preserve rejected approaches only when they explain an important decision.

---

## Compression Rules

- Be dense and information-rich.
- Remove greetings, filler, repetition, speculation, and abandoned exploration.
- Prefer structured state over narrative.
- Avoid duplicate information across sections.
- If information appears in multiple places, keep it in the most appropriate section.
- Prefer removing historical explanation over removing actionable state.
- Keep facts that affect future actions.
- Remove facts that only explain past discussion.
- Respect context limits: optimize for future execution, not completeness of history.

---

## Writing Style

- Use the language of the original conversation.
- Write as persistent agent memory.
- Use third-person past tense.
- Do not mention this compression process.
- Do not include analysis or justification about what was removed.

---

## Continuation Requirement

The resumed agent has no access to earlier messages except this anchor and the newest message group.

Every retained item must be:

- self-contained
- actionable when follow-up is required
- specific enough to continue execution without guessing

---

Output ONLY the updated anchor state.

Use these Markdown sections.
Omit any empty section.

## Intent

The original objective, refined goals, constraints, and success criteria.

## Immediate Next Step

The single most important concrete action to perform when resuming.

Must be:
- executable
- specific
- unambiguous

Do not list multiple options.

## Active Tasks

Unfinished work.

For each task include:
- current status
- next action
- blockers
- relevant context

## Completed Tasks

Finished work and verified outcomes.

Include:
- what was done
- resulting state
- evidence of completion

## Decisions

Important decisions and accepted approaches.

Include:
- chosen approach
- rejected alternatives when relevant
- rationale

## Invariants

Rules, constraints, and properties that future work must preserve.

Examples:
- compatibility requirements
- architectural constraints
- user preferences
- safety requirements
- forbidden changes

## Needed Skills

Skills, tools, libraries, APIs, or domain knowledge required for future work.

## Key Files

Files involved.

For each include:
- exact path
- status: created / modified / read / deleted
- purpose
- important changes

## Established Facts

Verified current state.

Include:
- environment details
- versions
- identifiers
- configuration
- discovered values
- confirmed behavior

## Pending Issues

Unresolved:
- errors
- blockers
- missing information
- unanswered questions

## Critical Artifacts

Only include exact text that must survive compression.

Examples:
- user requirements
- commands
- error messages
- configuration snippets
- identifiers

Preserve verbatim.
`
