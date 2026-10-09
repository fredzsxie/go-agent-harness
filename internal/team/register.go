package team

import (
	"go-agent-harness/internal/tool"
)

// RegisterLeadTools 将本模块的模型输入 schema 与执行函数一起注册；装配层只负责选择能力。
func RegisterLeadTools(registry *tool.Registry, runtime *Runtime) {
	agentName := map[string]any{"type": "string", "pattern": `^[A-Za-z0-9_-]{1,64}$`}
	taskID := map[string]any{"type": "string", "pattern": `^task_[0-9a-f]{8}$`}
	registry.Register(tool.Spec{Name: "spawn_teammate", Description: "Spawn a persistent teammate after the user confirms the proposed team.",
		Required: []string{"name", "role", "prompt"}, Properties: map[string]any{
			"name": agentName, "role": map[string]any{"type": "string", "minLength": 1},
			"prompt": map[string]any{"type": "string", "minLength": 1}, "task_id": taskID,
			"require_plan": map[string]any{"type": "boolean"},
		}}, runtime.RunSpawn)
	registry.Register(tool.Spec{Name: "list_teammates", Description: "List active persistent teammates."}, runtime.RunList)
	registry.Register(tool.Spec{Name: "send_message", Description: "Send an intermediate message to an active teammate.",
		Required: []string{"to", "content"}, Properties: map[string]any{
			"to": agentName, "content": map[string]any{"type": "string", "minLength": 1},
		}}, runtime.RunSend)
	registry.Register(tool.Spec{Name: "request_shutdown", Description: "Ask an active teammate to finish its current step and shut down.",
		Required: []string{"teammate"}, Properties: map[string]any{"teammate": agentName}}, runtime.RunRequestShutdown)
	registry.Register(tool.Spec{Name: "request_plan", Description: "Require a teammate plan before workspace changes.",
		Required: []string{"teammate", "task"}, Properties: map[string]any{
			"teammate": agentName, "task": map[string]any{"type": "string", "minLength": 1},
		}}, runtime.RunRequestPlan)
	registry.Register(tool.Spec{Name: "review_plan", Description: "Approve or reject the current plan for a teammate assignment.",
		Required: []string{"request_id", "approve"}, Properties: map[string]any{
			"request_id": map[string]any{"type": "string", "pattern": `^req_[0-9a-f]{8}$`},
			"approve":    map[string]any{"type": "boolean"}, "feedback": map[string]any{"type": "string"},
		}}, runtime.RunReviewPlan)
}
