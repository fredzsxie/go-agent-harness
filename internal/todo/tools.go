package todo

import (
	"go-agent-harness/internal/tool"
)

// RegisterTool 将本模块的模型输入 schema 与执行函数一起注册；装配层只负责选择能力。
func RegisterTool(registry *tool.Registry, manager *Manager) {
	registry.Register(tool.Spec{
		Name:        "todo_write",
		Description: "Create and manage a task list for your current coding session. Use this before and during multi-step work.",
		Required:    []string{"todos"},
		Properties: map[string]any{
			"todos": map[string]any{
				"type":     "array",
				"maxItems": 20,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"content": map[string]any{"type": "string", "minLength": 1},
						"status": map[string]any{
							"type": "string",
							"enum": []string{"pending", "in_progress", "completed"},
						},
					},
					"required": []string{"content", "status"},
				},
			},
		},
	}, manager.RunWrite)
}
