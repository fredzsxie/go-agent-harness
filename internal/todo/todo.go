package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

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
	return fmt.Sprintf("Updated %d tasks", len(todos)), nil
}

func (m *Manager) Snapshot() []Item {
	m.mu.Lock()
	defer m.mu.Unlock()

	clone := make([]Item, len(m.todos))
	copy(clone, m.todos)
	return clone
}

func normalizeTodos(value any) ([]Item, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("todos must be an array")
	}

	todos := make([]Item, 0, len(items))
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

		todos = append(todos, Item{
			Content: content,
			Status:  status,
		})
	}
	return todos, nil
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
	lines := []string{"", "## Current Tasks"}
	for _, todo := range todos {
		lines = append(lines, fmt.Sprintf("  [%s] %s", statusIcon(todo.Status), todo.Content))
	}
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
