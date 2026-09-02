package agent

import (
	"context"

	"go-agent-harness/internal/hooks"
)

// WorkerTurn 是一次 LLM 调用及其紧随的工具执行结果。
type WorkerTurn struct {
	Assistant Message
	Tools     ToolBatch
	HasTools  bool
}

// Worker 统一主 Agent 与 Subagent 的单轮模型和工具调用。
type Worker struct {
	model    Model
	registry *Registry
	executor *ToolExecutor
}

func NewWorker(model Model, registry *Registry, hookManager *hooks.Manager) *Worker {
	return &Worker{model: model, registry: registry, executor: NewToolExecutor(registry, hookManager)}
}

func (w *Worker) RunTurn(ctx context.Context, system string, messages []Message, intercept ToolInterceptor) (WorkerTurn, error) {
	response, err := w.model.Complete(ctx, ModelRequest{
		System: system, Messages: messages, Tools: w.registry.Specs(), MaxTokens: 8000,
	})
	if err != nil {
		return WorkerTurn{}, err
	}

	turn := WorkerTurn{Assistant: response.Message, HasTools: messageHasToolUse(response.Message)}
	if !turn.HasTools {
		return turn, nil
	}

	// 特殊工具可能依赖包含当前 assistant tool_use 的完整历史。
	history := append(CloneMessages(messages), response.Message)
	turn.Tools, err = w.executor.Execute(ctx, history, response.Message.Blocks, intercept)
	return turn, err
}
