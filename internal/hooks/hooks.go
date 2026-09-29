package hooks

import (
	"context"

	"go-agent-harness/internal/protocol"
)

type UserPromptSubmitHook func(query string)
type PreToolUseHook func(ctx context.Context, call ToolCall) string
type PostToolUseHook func(call ToolCall, output string)
type StopHook func(context.Context, StopContext) (StopDecision, error)

type ToolCall struct {
	ID    string
	Name  string
	Input map[string]any
}

type StopContext struct {
	ToolCallCnt int
	TurnCount   int
	TotalTokens int64
	Messages    []protocol.Message
}

type StopAction string

const (
	StopAllow    StopAction = "allow"
	StopBlock    StopAction = "block"
	StopDefer    StopAction = "defer"
	StopAchieved StopAction = "achieved"
	StopFailed   StopAction = "failed"
	StopLimit    StopAction = "limit"
	StopError    StopAction = "error"
)

// StopDecision 将“继续当前 Agent Loop”和“把控制权交还用户”明确分开。
// 只有 block 会立刻追加反馈并进入下一轮，其余 action 都结束本次 Run。
type StopDecision struct {
	Action StopAction
	Reason string
}

type Manager struct {
	userPromptSubmit []UserPromptSubmitHook
	preToolUse       []PreToolUseHook
	postToolUse      []PostToolUseHook
	stop             []StopHook
}

func NewManager() *Manager {
	return &Manager{
		userPromptSubmit: make([]UserPromptSubmitHook, 0, 4),
		preToolUse:       make([]PreToolUseHook, 0, 4),
		postToolUse:      make([]PostToolUseHook, 0, 4),
		stop:             make([]StopHook, 0, 4),
	}
}

func (m *Manager) OnUserPrompt(hook UserPromptSubmitHook) {
	m.userPromptSubmit = append(m.userPromptSubmit, hook)
}

func (m *Manager) BeforeTool(hook PreToolUseHook) {
	m.preToolUse = append(m.preToolUse, hook)
}

func (m *Manager) AfterTool(hook PostToolUseHook) {
	m.postToolUse = append(m.postToolUse, hook)
}

func (m *Manager) OnStop(hook StopHook) {
	m.stop = append(m.stop, hook)
}

func (m *Manager) TriggerUserPromptSubmit(query string) {
	for _, hook := range m.userPromptSubmit {
		hook(query)
	}
}

func (m *Manager) TriggerPreToolUse(ctx context.Context, call ToolCall) string {
	for _, hook := range m.preToolUse {
		if blocked := hook(ctx, call); blocked != "" {
			return blocked
		}
	}
	return ""
}

func (m *Manager) TriggerPostToolUse(call ToolCall, output string) {
	for _, hook := range m.postToolUse {
		hook(call, output)
	}
}

func (m *Manager) TriggerStop(ctx context.Context, input StopContext) (StopDecision, error) {
	for _, hook := range m.stop {
		decision, err := hook(ctx, input)
		if err != nil {
			return StopDecision{}, err
		}
		if decision.Action != "" && decision.Action != StopAllow {
			return decision, nil
		}
	}
	return StopDecision{Action: StopAllow}, nil
}
