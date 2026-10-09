// ToolExecutor 统一封装 tool_use 的执行流程。
package agent

import (
	"context"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/protocol"
	"go-agent-harness/internal/tool"
)

// ToolOutcome 表示工具调用结果及其对会话历史的影响。
type ToolOutcome struct {
	Text     string
	IsError  bool
	Stop     bool
	Messages []protocol.Message
	SkipPost bool
}

// ToolInterceptor 在 tool.Registry 分发前处理需要运行时状态的特殊工具。
type ToolInterceptor func(ctx context.Context, messages []protocol.Message, call hooks.ToolCall) (ToolOutcome, bool, error)

type ToolBatch struct {
	Results  []protocol.ContentBlock
	Count    int
	Stop     bool
	Messages []protocol.Message
}

type ToolExecutor struct {
	registry *tool.Registry
	hooks    *hooks.Manager
}

func NewToolExecutor(registry *tool.Registry, hookManager *hooks.Manager) *ToolExecutor {
	if hookManager == nil {
		hookManager = hooks.NewManager()
	}
	return &ToolExecutor{registry: registry, hooks: hookManager}
}

// Execute 对应 s02/s04：同一批 tool_use 按顺序执行，逐个经过权限 Hook 并生成匹配 ID 的结果。
// 普通工具失败转为 is_error 交还模型修正；只有编排错误才中断宿主循环。
func (e *ToolExecutor) Execute(ctx context.Context, messages []protocol.Message, blocks []protocol.ContentBlock, intercept ToolInterceptor) (ToolBatch, error) {
	batch := ToolBatch{Results: make([]protocol.ContentBlock, 0, len(blocks))}

	for _, block := range blocks {
		if block.Type != protocol.BlockToolUse {
			continue
		}
		batch.Count++

		call := hooks.ToolCall{
			ID:    block.ToolUseID,
			Name:  block.ToolName,
			Input: block.Input,
		}

		outcome := ToolOutcome{}
		if blocked := e.hooks.TriggerPreToolUse(ctx, call); blocked != "" {
			outcome.Text = blocked
			outcome.IsError = true
		} else {
			handled := false
			if intercept != nil {
				var err error
				outcome, handled, err = intercept(ctx, messages, call)
				if err != nil {
					return ToolBatch{}, err
				}
			}
			if !handled {
				output, err := e.registry.Dispatch(ctx, call.Name, call.Input)
				outcome.Text = output
				if err != nil {
					outcome.Text = err.Error()
					outcome.IsError = true
				}
			}
			if !outcome.SkipPost {
				e.hooks.TriggerPostToolUse(call, outcome.Text)
			}
		}

		batch.Results = append(batch.Results, protocol.ContentBlock{
			Type:      protocol.BlockToolResult,
			ToolUseID: call.ID,
			Text:      outcome.Text,
			IsError:   outcome.IsError,
		})
		if outcome.Stop {
			batch.Stop = true
			batch.Messages = outcome.Messages
			return batch, nil
		}
	}

	return batch, nil
}
