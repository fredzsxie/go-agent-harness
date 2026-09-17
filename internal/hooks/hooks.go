package hooks

import "context"

type UserPromptSubmitHook func(query string)
type PreToolUseHook func(ctx context.Context, call ToolCall) string
type PostToolUseHook func(call ToolCall, output string)
type StopHook func(ctx StopContext) string

type ToolCall struct {
	ID    string
	Name  string
	Input map[string]any
}

type StopContext struct {
	ToolCallCnt int
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

func (m *Manager) TriggerStop(ctx StopContext) string {
	for _, hook := range m.stop {
		if force := hook(ctx); force != "" {
			return force
		}
	}
	return ""
}
