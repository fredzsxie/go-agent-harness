package subagent

import (
	"go-agent-harness/internal/tool"
)

// RegisterTool 将本模块的模型输入 schema 与执行函数一起注册；装配层只负责选择能力。
func RegisterTool(registry *tool.Registry, manager *Manager) {
	registry.Register(tool.Spec{
		Name:        "task",
		Description: "Launch a subagent to handle a complex subtask. Returns only the final conclusion.",
		Required:    []string{"description"},
		Properties: map[string]any{
			"description": map[string]any{
				"type":        "string",
				"description": "Description of the subtask to delegate.",
			},
		},
	}, manager.RunTask)
}
