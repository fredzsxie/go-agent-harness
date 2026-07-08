// Package prompt 负责集中管理主代理与子代理的提示词片段，
// 为后续 skill、memory、retry 等动态提示拼装提供统一入口。
package prompt

import "strings"

const todoReminder = "<reminder>Update your todos.</reminder>"

func Main() string {
	return Build(
		"You are Claude Code, Anthropic's official CLI for Claude.",
		"You are a helpful coding assistant.",
		"For complex sub-problems, use task to spawn a subagent.",
		"Before starting any multi-step task, use todo_write to plan your steps and keep statuses updated.",
		"Only use emojis if the user explicitly requests it.",
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
	return todoReminder
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
