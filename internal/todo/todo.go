// Package todo 实现 s05 的会话内计划清单，不保存到磁盘，也不参与 s10 的任务依赖和认领。
package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const maxTodos = 20

type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
)

type Item struct {
	Content string `json:"content"`
	Status  Status `json:"status"`
}

type Manager struct {
	mu    sync.Mutex
	out   io.Writer
	todos []Item
}

func NewManager(out io.Writer) *Manager {
	return &Manager{
		out:   out,
		todos: make([]Item, 0),
	}
}

// RunWrite 先完整校验（最多 20 项、最多一项 in_progress），再一次替换旧清单。
// 校验失败保留原状态；每三轮提醒由 Runner 负责，Todo 本身不控制模型循环。
func (m *Manager) RunWrite(ctx context.Context, input any) (string, error) {
	_ = ctx
	payload, ok := input.(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid todo_write payload")
	}
	rawTodos, ok := payload["todos"]
	if !ok {
		return "", fmt.Errorf("missing todos")
	}

	todos, err := normalizeTodos(rawTodos)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	m.todos = todos
	rendered := renderTodos(todos)
	m.mu.Unlock()

	if m.out != nil {
		fmt.Fprintln(m.out, rendered)
	}
	return rendered, nil
}

func (m *Manager) Snapshot() []Item {
	m.mu.Lock()
	defer m.mu.Unlock()

	clone := make([]Item, len(m.todos))
	copy(clone, m.todos)
	return clone
}

func normalizeTodos(value any) ([]Item, error) {
	if encoded, ok := value.(string); ok {
		parsed, err := parseTodoString(encoded)
		if err != nil {
			return nil, err
		}
		value = parsed
	}

	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("todos must be an array")
	}
	if len(items) > maxTodos {
		return nil, fmt.Errorf("max %d todos allowed", maxTodos)
	}

	todos := make([]Item, 0, len(items))
	inProgress := 0
	for i, raw := range items {
		itemMap, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("todos[%d] must be an object", i)
		}

		content, _ := itemMap["content"].(string)
		content = strings.TrimSpace(content)
		if content == "" {
			return nil, fmt.Errorf("todos[%d] missing content", i)
		}

		statusValue, _ := itemMap["status"].(string)
		status := Status(strings.TrimSpace(statusValue))
		if !isValidStatus(status) {
			return nil, fmt.Errorf("todos[%d] has invalid status %q", i, statusValue)
		}
		if status == StatusInProgress {
			inProgress++
		}

		todos = append(todos, Item{
			Content: content,
			Status:  status,
		})
	}
	if inProgress > 1 {
		return nil, fmt.Errorf("only one todo can be in_progress at a time")
	}
	return todos, nil
}

func parseTodoString(encoded string) ([]any, error) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return nil, fmt.Errorf("todos must be an array")
	}

	var parsed []any
	if err := json.Unmarshal([]byte(encoded), &parsed); err == nil {
		return parsed, nil
	}
	// YAML 可安全解析部分 OpenAI-compatible provider 返回的单引号列表，且不会执行表达式。
	if err := yaml.Unmarshal([]byte(encoded), &parsed); err != nil {
		return nil, fmt.Errorf("todos must be an array or encoded array")
	}
	if parsed == nil {
		return nil, fmt.Errorf("todos must be an array or encoded array")
	}
	return parsed, nil
}

func isValidStatus(status Status) bool {
	switch status {
	case StatusPending, StatusInProgress, StatusCompleted:
		return true
	default:
		return false
	}
}

func renderTodos(todos []Item) string {
	if len(todos) == 0 {
		return "No todos."
	}
	lines := []string{"", "## Current Tasks"}
	done := 0
	for _, todo := range todos {
		lines = append(lines, fmt.Sprintf("  [%s] %s", statusIcon(todo.Status), todo.Content))
		if todo.Status == StatusCompleted {
			done++
		}
	}
	lines = append(lines, fmt.Sprintf("\n(%d/%d completed)", done, len(todos)))
	return strings.Join(lines, "\n")
}

func statusIcon(status Status) string {
	switch status {
	case StatusPending:
		return " "
	case StatusInProgress:
		return ">"
	case StatusCompleted:
		return "x"
	default:
		return "?"
	}
}

func (i Item) MarshalJSON() ([]byte, error) {
	type alias Item
	return json.Marshal(alias(i))
}
