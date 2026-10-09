// Package tool 实现 s02 的工具定义与名称分发，不依赖 Agent Loop 或模型供应商。
package tool

import "context"

// Spec 同时描述模型可见的能力和输入 schema；具体执行由对应 Handler 提供。
type Spec struct {
	Name        string
	Description string
	Required    []string
	Properties  map[string]any
}

// Handler 是所有内置工具和 MCP 适配器共用的执行边界。
// 工具错误交给 ToolExecutor 转成 tool_result，模型可在下一轮修正输入。
type Handler func(context.Context, any) (string, error)
