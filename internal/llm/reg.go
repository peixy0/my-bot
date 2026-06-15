package llm

import (
	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/runtime"
	"my-bot/internal/tasks"
	"my-bot/internal/tools"
)

func NewSessionRegistry(
	rt runtime.Runtime,
	skills *tools.SkillLoader,
	cfg *config.Config,
	agent *Agent,
	taskManager *tasks.Manager,
	cmdTools *tools.CommandToolset,
	sender events.Outbound,
) *tools.Registry {
	reg := tools.NewRegistry()
	registerBaseTools(reg, rt, skills, cfg, cmdTools)
	registerOutboundTools(reg, sender)
	RegisterMetaTools(reg, agent, skills, cfg, rt, taskManager)
	return reg
}

func NewSubagentRegistry(
	rt runtime.Runtime,
	skills *tools.SkillLoader,
	cfg *config.Config,
	agent *Agent,
	taskManager *tasks.Manager,
	cmdTools *tools.CommandToolset,
) *tools.Registry {
	reg := tools.NewRegistry()
	registerBaseTools(reg, rt, skills, cfg, cmdTools)
	RegisterMetaTools(reg, agent, skills, cfg, rt, taskManager)
	return reg
}

func registerBaseTools(reg *tools.Registry, rt runtime.Runtime, skills *tools.SkillLoader, cfg *config.Config, cmdTools *tools.CommandToolset) {
	reg.RegisterToolset(tools.NewDefaultToolset(rt, skills, cfg))
	reg.RegisterToolset(cmdTools)
}

func registerOutboundTools(reg *tools.Registry, sender events.Outbound) {
	registrar, ok := sender.(tools.ToolRegistrar)
	if !ok {
		return
	}
	registrar.RegisterTools(reg)
}
