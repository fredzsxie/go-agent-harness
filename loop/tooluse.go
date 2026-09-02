// Package loop 中的 ToolExecutor 统一封装 tool_use 的执行流程。
package loop

import (
	"context"

	"go-agent-harness/internal/hooks"
)

// ToolOutcome 表示拦截器生成的工具结果及其对会话历史的影响。
type ToolOutcome struct {
	Text     string
	IsError  bool
	Stop     bool
	Messages []Message
}

// ToolInterceptor 在 Registry 分发前处理需要运行时状态的特殊工具。
type ToolInterceptor func(ctx context.Context, messages []Message, call hooks.ToolCall) (ToolOutcome, bool, error)

type ToolBatch struct {
	Results  []ContentBlock
	Count    int
	Stop     bool
	Messages []Message
}

type ToolExecutor struct {
	registry *Registry
	hooks    *hooks.Manager
}

func NewToolExecutor(registry *Registry, hookManager *hooks.Manager) *ToolExecutor {
	if hookManager == nil {
		hookManager = hooks.NewManager()
	}
	return &ToolExecutor{registry: registry, hooks: hookManager}
}

func (e *ToolExecutor) Execute(ctx context.Context, messages []Message, blocks []ContentBlock, intercept ToolInterceptor) (ToolBatch, error) {
	batch := ToolBatch{Results: make([]ContentBlock, 0, len(blocks))}

	for _, block := range blocks {
		if block.Type != BlockToolUse {
			continue
		}
		batch.Count++

		call := hooks.ToolCall{
			ID:    block.ToolUseID,
			Name:  block.ToolName,
			Input: block.Input,
		}

		outcome := ToolOutcome{}
		if blocked := e.hooks.TriggerPreToolUse(call); blocked != "" {
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
			e.hooks.TriggerPostToolUse(call, outcome.Text)
		}

		batch.Results = append(batch.Results, ContentBlock{
			Type:      BlockToolResult,
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
