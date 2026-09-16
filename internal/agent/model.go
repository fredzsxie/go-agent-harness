package agent

import (
	"context"

	"go-agent-harness/internal/protocol"
)

// ToolSpec 描述可提供给 LLM 的工具及其输入 schema。
type ToolSpec struct {
	Name        string
	Description string
	Required    []string
	Properties  map[string]any
}

// ModelRequest 是 Agent 发给模型的供应商无关请求。
type ModelRequest struct {
	System    string
	Messages  []protocol.Message
	Tools     []ToolSpec
	MaxTokens int64
	Model     string
}

// ModelResponse 保留 Agent Loop 推进消息和判断截断恢复所需的响应内容。
type ModelResponse struct {
	Message    protocol.Message
	StopReason string
}

// ModelError 在不泄露具体 SDK 类型的前提下传递 HTTP 错误状态。
type ModelError struct {
	HTTPStatus int
	Err        error
}

func (e *ModelError) Error() string { return e.Err.Error() }
func (e *ModelError) Unwrap() error { return e.Err }

type RunResult struct {
	Messages []protocol.Message
	Output   string
}

// Model 隔离具体 LLM SDK，使主 Agent、Subagent 和上下文能力复用同一调用边界。
type Model interface {
	Complete(ctx context.Context, request ModelRequest) (ModelResponse, error)
}
