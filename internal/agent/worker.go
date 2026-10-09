package agent

import (
	"context"
	"errors"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/protocol"
	"go-agent-harness/internal/tool"
)

// WorkerTurn 是一次 LLM 调用及其紧随的工具执行结果。
type WorkerTurn struct {
	Assistant  protocol.Message
	Tools      ToolBatch
	HasTools   bool
	StopReason string
	Usage      llm.Usage
}

// Truncated 表示响应尚不完整，不能执行其中的工具或把它作为完成结果。
func (t WorkerTurn) Truncated() bool { return t.StopReason == "max_tokens" }

var ErrResponseTruncated = errors.New("model response remains truncated after increasing max_tokens")

type TurnOptions struct {
	Model     string
	MaxTokens int64
}

const DefaultMaxTokens int64 = 8000

// Worker 统一主 Agent 与 Subagent 的单轮模型和工具调用。
type Worker struct {
	model    llm.Model
	registry *tool.Registry
	executor *ToolExecutor
}

func NewWorker(model llm.Model, registry *tool.Registry, hookManager *hooks.Manager) *Worker {
	return &Worker{model: model, registry: registry, executor: NewToolExecutor(registry, hookManager)}
}

func (w *Worker) RunTurn(ctx context.Context, system string, messages []protocol.Message, intercept ToolInterceptor) (WorkerTurn, error) {
	// s06/s13 不持有主 Runner 的恢复状态，因此从相同输入扩容重试一次。
	// 截断片段不写入历史，也不执行工具；只有完整响应才能产生副作用。
	turn, err := w.RunTurnWithOptions(ctx, system, messages, intercept, TurnOptions{})
	if err != nil || !turn.Truncated() {
		return turn, err
	}
	usage := turn.Usage
	logger.Warn("[LLM] worker response truncated, retrying with max_tokens=%d", escalatedMaxTokens)
	turn, err = w.RunTurnWithOptions(ctx, system, messages, intercept, TurnOptions{MaxTokens: escalatedMaxTokens})
	usage.Add(turn.Usage)
	turn.Usage = usage
	if err == nil && turn.Truncated() {
		logger.Warn("[LLM] worker response is still truncated")
		return turn, ErrResponseTruncated
	}
	return turn, err
}

// RunTurnWithOptions 允许主 Agent 在恢复期间切换模型或 token 上限。
func (w *Worker) RunTurnWithOptions(ctx context.Context, system string, messages []protocol.Message, intercept ToolInterceptor, options TurnOptions) (WorkerTurn, error) {
	if options.MaxTokens <= 0 {
		options.MaxTokens = DefaultMaxTokens
	}
	response, err := w.model.Complete(ctx, llm.Request{
		System: system, Messages: messages, Tools: w.registry.Specs(), MaxTokens: options.MaxTokens, Model: options.Model,
	})
	if err != nil {
		return WorkerTurn{}, err
	}

	turn := WorkerTurn{
		Assistant: response.Message, HasTools: messageHasToolUse(response.Message),
		StopReason: response.StopReason, Usage: response.Usage,
	}
	// 此底层入口只执行一次模型请求；RunTurn 或主 Runner 必须先恢复截断，再推进历史。
	if !turn.HasTools || turn.Truncated() {
		return turn, nil
	}

	// 特殊工具可能依赖包含当前 assistant tool_use 的完整历史。
	history := append(protocol.CloneMessages(messages), response.Message)
	turn.Tools, err = w.executor.Execute(ctx, history, response.Message.Blocks, intercept)
	return turn, err
}
