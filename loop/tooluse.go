// Package loop 中的 tooluse 执行器统一封装 tool_use 的执行流程，
// 包括 hook 触发、registry 调度和 tool_result 回填。
package loop

import (
	"context"

	"go-agent-harness/internal/hooks"
)

type ToolObserver func(call hooks.ToolCall, result string, isError bool)

func ExecuteToolUses(ctx context.Context, blocks []ContentBlock, registry *Registry, hookManager *hooks.Manager, observer ToolObserver) ([]ContentBlock, int) {
	results := make([]ContentBlock, 0, len(blocks))
	toolCallCnt := 0

	for _, block := range blocks {
		if block.Type != BlockToolUse {
			continue
		}
		toolCallCnt++

		call := hooks.ToolCall{
			ID:    block.ToolUseID,
			Name:  block.ToolName,
			Input: block.Input,
		}

		result := ""
		isError := false
		if blocked := hookManager.TriggerPreToolUse(call); blocked != "" {
			result = blocked
			isError = true
		} else {
			output, err := registry.Dispatch(ctx, call.Name, call.Input)
			if err != nil {
				output = err.Error()
				isError = true
			}
			result = output
			hookManager.TriggerPostToolUse(call, result)
			if observer != nil {
				observer(call, result, isError)
			}
		}

		results = append(results, ContentBlock{
			Type:      BlockToolResult,
			ToolUseID: call.ID,
			Text:      result,
			IsError:   isError,
		})
	}

	return results, toolCallCnt
}
