package goal

import (
	"context"
	"time"

	"go-agent-harness/internal/protocol"
)

const (
	DefaultBlockCap = 8
	MaxConditionLen = 4000
)

// Evaluation 是独立 evaluator 对完成条件的判断。
type Evaluation struct {
	OK         bool
	Reason     string
	Impossible bool
}

// Evaluator 只读取完成条件和对话证据，不获得主 Agent 的工具。
type Evaluator interface {
	Evaluate(context.Context, string, []protocol.Message) (Evaluation, error)
}

// State 保存当前 Session 中唯一一个活动 Goal。
type State struct {
	Condition     string
	Iterations    int
	SetAt         time.Time
	TokensAtStart int64
	LastReason    string
}

// Event 是可由 Host 持久化的 Goal 状态事件。
type Event struct {
	Type            string `json:"type"`
	Condition       string `json:"condition"`
	Active          bool   `json:"active"`
	Met             bool   `json:"met"`
	Failed          bool   `json:"failed"`
	Reason          string `json:"reason"`
	Iterations      int    `json:"iterations"`
	DurationSeconds int64  `json:"durationSeconds"`
}

type Config struct {
	Evaluator Evaluator
	BlockCap  int
	Now       func() time.Time
}
