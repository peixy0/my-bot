package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type SkillSummary struct {
	Name        string
	Description string
}

type Skill struct {
	Name         string
	Dir          string
	Description  string
	Instructions string
}

type SkillLoader struct {
	skillsDir string
}

func NewSkillLoader(skillsDir string) *SkillLoader {
	return &SkillLoader{skillsDir: skillsDir}
}

func (l *SkillLoader) Discover() []SkillSummary {
	entries, err := os.ReadDir(l.skillsDir)
	if err != nil {
		return nil
	}
	var summaries []SkillSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(l.skillsDir, e.Name(), "SKILL.md")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fm, _ := ParseFrontmatter(string(data))
		summaries = append(summaries, SkillSummary{
			Name:        e.Name(),
			Description: fm["description"],
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Name < summaries[j].Name
	})
	return summaries
}

func (l *SkillLoader) Load(name string) (*Skill, error) {
	path := filepath.Join(l.skillsDir, name, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skill %q not found", name)
	}
	fm, body := ParseFrontmatter(string(data))
	return &Skill{
		Name:         name,
		Dir:          filepath.Join(l.skillsDir, name),
		Description:  fm["description"],
		Instructions: strings.TrimSpace(body),
	}, nil
}

func registerSkillTool(r *Registry, loader *SkillLoader) {
	r.Register(ToolSchema{
		Name:        "use_skill",
		Description: "Load detailed instructions for a named skill and return them for your context.\n\nCall this at the start of any task that matches a skill in your available skills list. Pass the exact skill name from the list.",
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill_name": map[string]any{
					"type":        "string",
					"description": "The exact name of the skill to load, as listed in the available skills section of your context (e.g. 'commit', 'review-pr'). Returns the full instruction text for that skill.",
				},
			},
			"required": []string{"skill_name"},
		}),
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			SkillName string `json:"skill_name"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, fmt.Errorf("parse use_skill args: %w", err)
		}
		skill, err := loader.Load(p.SkillName)
		if err != nil {
			return ToolResult{}, err
		}
		return TextResult(formatSkillResult(skill)), nil
	})
}
