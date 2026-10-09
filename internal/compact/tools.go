package compact

import (
	"context"

	"go-agent-harness/internal/tool"
)

// RegisterTool 将本模块的模型输入 schema 与执行函数一起注册；装配层只负责选择能力。
func RegisterTool(registry *tool.Registry) {
	registry.Register(tool.Spec{
		Name:        "compact",
		Description: "Summarize earlier conversation to free context space.",
		Properties: map[string]any{
			"focus": map[string]any{"type": "string", "description": "Optional focus for what the summary should preserve."},
		},
	}, func(context.Context, any) (string, error) {
		return "[compact is handled by the runner]", nil
	})
}
