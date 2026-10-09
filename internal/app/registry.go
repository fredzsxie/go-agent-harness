package app

import (
	"go-agent-harness/internal/tool"
	"go-agent-harness/internal/tool/builtin"
	"go-agent-harness/internal/workspace"
)

// 两个工具池共享实现，但 s06 不具备递归委派和 s11 后台启动能力。
func newDefaultRegistry(resolver *workspace.Resolver) *tool.Registry {
	registry := tool.NewRegistry()
	builtin.RegisterTools(registry, builtin.New(resolver), true)
	return registry
}
func newSubagentRegistry(resolver *workspace.Resolver) *tool.Registry {
	registry := tool.NewRegistry()
	builtin.RegisterTools(registry, builtin.New(resolver), false)
	return registry
}
