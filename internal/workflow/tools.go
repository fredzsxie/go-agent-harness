package workflow

import (
	"go-agent-harness/internal/tool"
)

// RegisterTool 将本模块的模型输入 schema 与执行函数一起注册；装配层只负责选择能力。
func RegisterTool(registry *tool.Registry, manager *Manager) {
	registry.Register(tool.Spec{
		Name:        "workflow",
		Description: "Run a Host-registered workflow by name. Pass all source material through args.",
		Required:    []string{"name"},
		Properties: map[string]any{
			"name":               map[string]any{"type": "string", "enum": manager.Names()},
			"args":               map[string]any{"type": "object", "description": "JSON-safe workflow arguments."},
			"resume_from_run_id": map[string]any{"type": "string", "pattern": `^wf_[A-Za-z0-9][A-Za-z0-9._-]{0,63}_[0-9a-f]{16}$`},
		},
	}, manager.RunTool)
}
