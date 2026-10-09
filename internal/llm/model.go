// Package llm 定义供应商无关的模型调用协议。主循环、上下文辅助调用和独立评估器只依赖这里。
package llm

import (
	"context"

	"go-agent-harness/internal/protocol"
	"go-agent-harness/internal/tool"
)

// Request 是 Agent 发给模型的供应商无关请求。
type Request struct {
	System    string
	Messages  []protocol.Message
	Tools     []tool.Spec
	MaxTokens int64
	Model     string
}

// Usage 记录单次模型调用消耗的输入与输出 token。
type Usage struct {
	InputTokens  int64
	OutputTokens int64
}

func (u Usage) Total() int64 {
	return u.InputTokens + u.OutputTokens
}

func (u *Usage) Add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
}

// Response 保留 Agent Loop 推进消息和判断截断恢复所需的响应内容。
type Response struct {
	Message    protocol.Message
	StopReason string
	Usage      Usage
}

// Error 在不泄露具体 SDK 类型的前提下传递 HTTP 错误状态。
type Error struct {
	HTTPStatus int
	Err        error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Model 隔离具体 LLM SDK，使主 Agent、Subagent 和上下文能力复用同一调用边界。
type Model interface {
	Complete(ctx context.Context, request Request) (Response, error)
}
