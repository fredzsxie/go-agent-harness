package agent

import (
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/protocol"
)

// RunResult 是一次 Session 提交的结果；Usage 只累计主循环调用，Stop 保留停止原因。
type RunResult struct {
	Messages []protocol.Message
	Output   string
	Usage    llm.Usage
	Stop     hooks.StopDecision
}
