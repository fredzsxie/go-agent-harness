package agent

import (
	"context"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/protocol"
)

// WorkerTurn 是一次 LLM 调用及其紧随的工具执行结果。
type WorkerTurn struct {
	Assistant  protocol.Message
	Tools      ToolBatch
	HasTools   bool
	StopReason string
}

type TurnOptions struct {
	Model     string
	MaxTokens int64
}

const DefaultMaxTokens int64 = 8000

// Worker 统一主 Agent 与 Subagent 的单轮模型和工具调用。
type Worker struct {
	model    Model
	registry *Registry
	executor *ToolExecutor
}

func NewWorker(model Model, registry *Registry, hookManager *hooks.Manager) *Worker {
	return &Worker{model: model, registry: registry, executor: NewToolExecutor(registry, hookManager)}
}

func (w *Worker) RunTurn(ctx context.Context, system string, messages []protocol.Message, intercept ToolInterceptor) (WorkerTurn, error) {
	return w.RunTurnWithOptions(ctx, system, messages, intercept, TurnOptions{})
}

// RunTurnWithOptions 允许主 Agent 在恢复期间切换模型或 token 上限。
func (w *Worker) RunTurnWithOptions(ctx context.Context, system string, messages []protocol.Message, intercept ToolInterceptor, options TurnOptions) (WorkerTurn, error) {
	if options.MaxTokens <= 0 {
		options.MaxTokens = DefaultMaxTokens
	}
	response, err := w.model.Complete(ctx, ModelRequest{
		System: system, Messages: messages, Tools: w.registry.Specs(), MaxTokens: options.MaxTokens, Model: options.Model,
	})
	if err != nil {
		return WorkerTurn{}, err
	}

	turn := WorkerTurn{Assistant: response.Message, HasTools: messageHasToolUse(response.Message), StopReason: response.StopReason}
	// max_tokens 可能截断 tool_use 参数，必须先由 Runner 恢复完整响应。
	if !turn.HasTools || turn.StopReason == "max_tokens" {
		return turn, nil
	}

	// 特殊工具可能依赖包含当前 assistant tool_use 的完整历史。
	history := append(CloneMessages(messages), response.Message)
	turn.Tools, err = w.executor.Execute(ctx, history, response.Message.Blocks, intercept)
	return turn, err
}
