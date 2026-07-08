// Package prompt 负责集中管理主代理与子代理的提示词片段，
// 为后续 skill、memory、retry 等动态提示拼装提供统一入口。
package prompt

import "strings"

func Main(skillCatalog string) string {
	if strings.TrimSpace(skillCatalog) == "" {
		skillCatalog = "(no skills found)"
	}
	return Build(
		"You are Claude Code, Anthropic's official CLI for Claude.",
		"You are a helpful coding assistant.",
		"For complex sub-problems, use task to spawn a subagent.",
		"Before starting any multi-step task, use todo_write to plan your steps and keep statuses updated.",
		"Skills available:\n"+skillCatalog,
		"Use load_skill to get full details when needed.",
	)
}

func Subagent() string {
	return Build(
		"You are a coding assistant.",
		"Complete the task you were given, then return a concise summary.",
		"Do not delegate further.",
	)
}

func TodoReminder() string {
	return "<reminder>Update your todos.</reminder>"
}

func Build(sections ...string) string {
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}
		parts = append(parts, section)
	}
	return strings.Join(parts, "\n")
}
