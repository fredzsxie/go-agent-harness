package hooks

import "fmt"

type Event string

const (
	EventUserPromptSubmit Event = "UserPromptSubmit"
	EventPreToolUse       Event = "PreToolUse"
	EventPostToolUse      Event = "PostToolUse"
	EventStop             Event = "Stop"
)

type UserPromptSubmitHook func(query string)
type PreToolUseHook func(call ToolCall) string
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

func (m *Manager) Register(event Event, callback any) error {
	switch event {
	case EventUserPromptSubmit:
		hook, ok := callback.(func(string))
		if !ok {
			return fmt.Errorf("invalid callback for %s", event)
		}
		m.userPromptSubmit = append(m.userPromptSubmit, hook)
	case EventPreToolUse:
		hook, ok := callback.(func(ToolCall) string)
		if !ok {
			return fmt.Errorf("invalid callback for %s", event)
		}
		m.preToolUse = append(m.preToolUse, hook)
	case EventPostToolUse:
		hook, ok := callback.(func(ToolCall, string))
		if !ok {
			return fmt.Errorf("invalid callback for %s", event)
		}
		m.postToolUse = append(m.postToolUse, hook)
	case EventStop:
		hook, ok := callback.(func(StopContext) string)
		if !ok {
			return fmt.Errorf("invalid callback for %s", event)
		}
		m.stop = append(m.stop, hook)
	default:
		return fmt.Errorf("unknown hook event: %s", event)
	}
	return nil
}

func (m *Manager) TriggerUserPromptSubmit(query string) {
	for _, hook := range m.userPromptSubmit {
		hook(query)
	}
}

func (m *Manager) TriggerPreToolUse(call ToolCall) string {
	for _, hook := range m.preToolUse {
		if blocked := hook(call); blocked != "" {
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
