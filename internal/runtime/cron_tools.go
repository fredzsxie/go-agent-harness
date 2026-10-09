package runtime

import (
	"context"
	"fmt"
	"strings"

	"go-agent-harness/internal/tool"
)

// RegisterCronTools 将本模块的模型输入 schema 与执行函数一起注册；装配层只负责选择能力。
func RegisterCronTools(registry *tool.Registry, manager *CronScheduler) {
	registry.Register(tool.Spec{
		Name:        "schedule_cron",
		Description: "Schedule a prompt using a five-field cron expression in the Agent process's local time.",
		Required:    []string{"cron", "prompt"},
		Properties: map[string]any{
			"cron":      map[string]any{"type": "string", "description": "Five fields: minute hour day month weekday."},
			"prompt":    map[string]any{"type": "string", "minLength": 1, "description": "Work for the Agent to start when due."},
			"recurring": map[string]any{"type": "boolean", "description": "Repeat on future matches; defaults to true."},
			"durable":   map[string]any{"type": "boolean", "description": "Persist across process restarts; defaults to true."},
		},
	}, manager.RunSchedule)
	registry.Register(tool.Spec{Name: "list_crons", Description: "List scheduled cron jobs."}, manager.RunList)
	registry.Register(tool.Spec{
		Name:        "cancel_cron",
		Description: "Cancel a scheduled cron job and any pending delivery.",
		Required:    []string{"job_id"},
		Properties: map[string]any{
			"job_id": map[string]any{"type": "string", "pattern": `^cron_[0-9a-f]{8}$`},
		},
	}, manager.RunCancel)
}

func (m *CronScheduler) RunSchedule(_ context.Context, input any) (string, error) {
	payload, _ := input.(map[string]any)
	expression, _ := payload["cron"].(string)
	prompt, _ := payload["prompt"].(string)
	recurring, err := optionalBool(payload, "recurring", true)
	if err != nil {
		return "", err
	}
	durable, err := optionalBool(payload, "durable", true)
	if err != nil {
		return "", err
	}
	job, err := m.Schedule(expression, prompt, recurring, durable)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Scheduled %s: %s -> %s", job.ID, job.Cron, job.Prompt), nil
}

func (m *CronScheduler) RunList(context.Context, any) (string, error) {
	jobs := m.List()
	if len(jobs) == 0 {
		return "No cron jobs.", nil
	}
	lines := make([]string, 0, len(jobs))
	for _, job := range jobs {
		frequency := "one-shot"
		if job.Recurring {
			frequency = "recurring"
		}
		storage := "session"
		if job.Durable {
			storage = "durable"
		}
		lines = append(lines, fmt.Sprintf("%s: %s -> %s [%s, %s]", job.ID, job.Cron, preview(job.Prompt, 60), frequency, storage))
	}
	return strings.Join(lines, "\n"), nil
}

func (m *CronScheduler) RunCancel(_ context.Context, input any) (string, error) {
	payload, _ := input.(map[string]any)
	id, _ := payload["job_id"].(string)
	if err := m.Cancel(id); err != nil {
		return "", err
	}
	return "Cancelled " + id, nil
}

func optionalBool(payload map[string]any, key string, fallback bool) (bool, error) {
	value, exists := payload[key]
	if !exists {
		return fallback, nil
	}
	parsed, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return parsed, nil
}
