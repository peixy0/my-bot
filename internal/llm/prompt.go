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

var baseSections = []string{"PERSONA.md", "RULES.md", "CONTEXT.md", "TOOLS.md"}

type MainPrompt struct{ promptBase }

func NewMainPrompt(skills *tools.SkillLoader, rt runtime.Runtime) *MainPrompt {
	return &MainPrompt{promptBase{skills, rt}}
}

func (p *MainPrompt) Build(ctx context.Context) string {
	return p.compose(ctx, baseSections...)
}

type HeartbeatPrompt struct{ promptBase }

func NewHeartbeatPrompt(skills *tools.SkillLoader, rt runtime.Runtime) *HeartbeatPrompt {
	return &HeartbeatPrompt{promptBase{skills, rt}}
}

func (p *HeartbeatPrompt) Build(ctx context.Context) string {
	return p.compose(ctx, append(baseSections, "HEARTBEAT.md")...)
}

type CronPrompt struct{ promptBase }

func NewCronPrompt(skills *tools.SkillLoader, rt runtime.Runtime) *CronPrompt {
	return &CronPrompt{promptBase{skills, rt}}
}

func (p *CronPrompt) Build(ctx context.Context) string {
	return p.compose(ctx, append(baseSections, "CRON.md")...)
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
