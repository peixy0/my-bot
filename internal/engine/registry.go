package engine

import (
	"my-bot/internal/browser"
	"my-bot/internal/config"
	"my-bot/internal/events"
	"my-bot/internal/runtime"
	"my-bot/internal/tasks"
	"my-bot/internal/tools"
)

func NewSessionRegistry(
	env SessionEnv,
	taskManager *tasks.Manager,
	cmdTools *tools.CommandToolset,
	browserClient browser.Client,
	sender events.Outbound,
) *tools.Registry {
	reg := tools.NewRegistry()
	registerBaseTools(reg, env.Rt, env.Skills, env.Cfg, cmdTools)
	browserClient.Register(reg)
	registerOutboundTools(reg, sender)
	registerMetaTools(reg, env, taskManager)
	return reg
}

func NewSubagentRegistry(
	rt runtime.Runtime,
	skills *tools.SkillLoader,
	cfg *config.Config,
	cmdTools *tools.CommandToolset,
	browserClient browser.Client,
) *tools.Registry {
	reg := tools.NewRegistry()
	registerBaseTools(reg, rt, skills, cfg, cmdTools)
	browserClient.Register(reg)
	return reg
}

func registerMetaTools(r *tools.Registry, env SessionEnv, taskManager *tasks.Manager) {
	NewSubagentToolset(env, taskManager).Register(r)
	NewFleetToolset(env, taskManager).Register(r)
}

func registerBaseTools(reg *tools.Registry, rt runtime.Runtime, skills *tools.SkillLoader, cfg *config.Config, cmdTools *tools.CommandToolset) {
	reg.RegisterToolset(tools.NewDefaultToolset(rt, skills, cfg))
	reg.RegisterToolset(cmdTools)
}

func registerOutboundTools(reg *tools.Registry, sender events.Outbound) {
	toolset, ok := sender.(tools.Toolset)
	if !ok {
		return
	}
	toolset.Register(reg)
}
