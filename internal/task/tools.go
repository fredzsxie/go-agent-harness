package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go-agent-harness/internal/tool"
)

// RegisterTools 将本模块的模型输入 schema 与执行函数一起注册；装配层只负责选择能力。
func RegisterTools(registry *tool.Registry, manager *Manager) {
	// 任务节点必须先创建再建立依赖，因此 schema 明确约束运行时任务 ID。
	taskID := map[string]any{"type": "string", "pattern": `^task_[0-9a-f]{8}$`}
	registry.Register(tool.Spec{
		Name: "create_task", Description: "Create a persistent pending task. Create all task nodes before adding dependencies.",
		Required: []string{"subject"}, Properties: map[string]any{
			"subject":     map[string]any{"type": "string", "minLength": 1},
			"description": map[string]any{"type": "string"},
		},
	}, manager.RunCreate)
	registry.Register(tool.Spec{
		Name: "update_task", Description: "Add dependencies to an unowned pending task using IDs returned by create_task.",
		Required: []string{"task_id", "addBlockedBy"}, Properties: map[string]any{
			"task_id":      taskID,
			"addBlockedBy": map[string]any{"type": "array", "minItems": 1, "items": taskID},
		},
	}, manager.RunUpdate)
	registry.Register(tool.Spec{Name: "list_tasks", Description: "List persistent tasks and their states."}, manager.RunList)
	registry.Register(tool.Spec{
		Name: "get_task", Description: "Get one persistent task by ID.",
		Required: []string{"task_id"}, Properties: map[string]any{"task_id": taskID},
	}, manager.RunGet)
	registry.Register(tool.Spec{
		Name: "claim_task", Description: "Claim a pending task after all dependencies are completed.",
		Required: []string{"task_id"}, Properties: map[string]any{"task_id": taskID},
	}, manager.RunClaim)
	registry.Register(tool.Spec{
		Name: "complete_task", Description: "Complete a task owned by this agent and report newly unblocked tasks.",
		Required: []string{"task_id"}, Properties: map[string]any{"task_id": taskID},
	}, manager.RunComplete)
}

// RunCreate 将 create_task 工具参数转换为任务创建操作。
func (m *Manager) RunCreate(_ context.Context, input any) (string, error) {
	args, err := object(input)
	if err != nil {
		return "", err
	}
	subject, err := requiredString(args, "subject")
	if err != nil {
		return "", err
	}
	description, _ := args["description"].(string)
	task, err := m.Create(subject, description)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Created %s: %s", task.ID, task.Subject), nil
}

// RunUpdate 将 update_task 工具参数转换为依赖更新操作。
func (m *Manager) RunUpdate(_ context.Context, input any) (string, error) {
	args, err := object(input)
	if err != nil {
		return "", err
	}
	id, err := requiredString(args, "task_id")
	if err != nil {
		return "", err
	}
	dependencies, err := stringList(args, "addBlockedBy")
	if err != nil {
		return "", err
	}
	task, err := m.AddBlockedBy(id, dependencies)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Updated %s blockedBy: %s", id, strings.Join(task.BlockedBy, ", ")), nil
}

// RunList 返回适合模型读取的单行任务摘要列表。
func (m *Manager) RunList(_ context.Context, _ any) (string, error) {
	tasks, err := m.List()
	if err != nil {
		return "", err
	}
	return FormatList(tasks), nil
}

// FormatList 在 Lead 与 Teammate 间共享展示格式，不暴露工具 Handler 或存储实现。
func FormatList(tasks []Task) string {
	if len(tasks) == 0 {
		return "No tasks. Use create_task to add some."
	}
	lines := make([]string, 0, len(tasks))
	markers := map[Status]string{Pending: "[ ]", InProgress: "[>]", Completed: "[x]"}
	for _, task := range tasks {
		line := fmt.Sprintf("%s %s: %s [%s]", markers[task.Status], task.ID, task.Subject, task.Status)
		if task.Owner != nil {
			line += " [" + *task.Owner + "]"
		}
		if len(task.BlockedBy) > 0 {
			line += " (blockedBy: " + strings.Join(task.BlockedBy, ", ") + ")"
		}
		if task.Worktree != nil {
			line += " (worktree: " + *task.Worktree + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// RunGet 返回指定任务的完整 JSON 内容。
func (m *Manager) RunGet(_ context.Context, input any) (string, error) {
	id, err := taskID(input)
	if err != nil {
		return "", err
	}
	task, err := m.Get(id)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(task, "", "  ")
	return string(data), err
}

// RunClaim 使用固定 AgentOwner 认领任务。
func (m *Manager) RunClaim(_ context.Context, input any) (string, error) {
	id, err := taskID(input)
	if err != nil {
		return "", err
	}
	task, err := m.Claim(id, AgentOwner)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Claimed %s (%s)", task.ID, task.Subject), nil
}

// RunComplete 使用固定 AgentOwner 完成任务并报告新解锁任务。
func (m *Manager) RunComplete(_ context.Context, input any) (string, error) {
	id, err := taskID(input)
	if err != nil {
		return "", err
	}
	task, unblocked, err := m.Complete(id, AgentOwner)
	if err != nil {
		return "", err
	}
	result := fmt.Sprintf("Completed %s (%s)", task.ID, task.Subject)
	if len(unblocked) > 0 {
		subjects := make([]string, len(unblocked))
		for i := range unblocked {
			subjects[i] = unblocked[i].Subject
		}
		result += "\nUnblocked: " + strings.Join(subjects, ", ")
	}
	return result, nil
}

func object(input any) (map[string]any, error) {
	args, ok := input.(map[string]any)
	if !ok {
		return nil, errors.New("tool input must be an object")
	}
	return args, nil
}

func requiredString(args map[string]any, key string) (string, error) {
	value, ok := args[key].(string)
	value = strings.TrimSpace(value)
	if !ok || value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func stringList(args map[string]any, key string) ([]string, error) {
	values, ok := args[key].([]any)
	if !ok || len(values) == 0 {
		return nil, fmt.Errorf("%s must be a non-empty array", key)
	}
	result := make([]string, len(values))
	for i, value := range values {
		text, ok := value.(string)
		if !ok || !taskIDPattern.MatchString(text) {
			return nil, fmt.Errorf("%s contains an invalid task id", key)
		}
		result[i] = text
	}
	return result, nil
}

func taskID(input any) (string, error) {
	args, err := object(input)
	if err != nil {
		return "", err
	}
	return requiredString(args, "task_id")
}
