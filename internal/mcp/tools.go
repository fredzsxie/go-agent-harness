package mcp

import (
	"go-agent-harness/internal/tool"
)

// RegisterTool 将本模块的模型输入 schema 与执行函数一起注册；装配层只负责选择能力。
func RegisterTool(registry *tool.Registry, manager *Manager) {
	registry.Register(tool.Spec{
		Name:        "connect_mcp",
		Description: "Connect to an MCP server and discover its tools.",
		Required:    []string{"name"},
		Properties: map[string]any{
			"name": map[string]any{"type": "string", "enum": manager.Available()},
		},
	}, manager.RunConnect)
}
