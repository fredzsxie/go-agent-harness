package app

import (
	"context"

	agentruntime "go-agent-harness/internal/runtime"
	"go-agent-harness/internal/skill"
	"go-agent-harness/internal/subagent"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/todo"
	"go-agent-harness/loop"
	"go-agent-harness/tools"
)

func newDefaultRegistry() *loop.Registry {
	registry := loop.NewRegistry()
	registerBaseTools(registry, true)
	return registry
}

func newSubagentRegistry() *loop.Registry {
	registry := loop.NewRegistry()
	registerBaseTools(registry, false)
	return registry
}

func registerBaseTools(registry *loop.Registry, allowBackground bool) {
	// 后台参数只暴露给主 Agent；subagent 仍使用同步 Bash，避免声明未接入的能力。
	bashDescription := "Run a shell command in the current workspace. Prefer read_file/glob for inspection when possible."
	bashProperties := map[string]any{
		"command": map[string]any{"type": "string", "description": "Shell command to execute."},
	}
	if allowBackground {
		bashDescription = "Run a shell command in the current workspace. Set run_in_background to true only for an independent slow command."
		bashProperties["run_in_background"] = map[string]any{"type": "boolean", "description": "Run asynchronously and collect the result on a later turn."}
	}
	registry.Register(loop.ToolSpec{
		Name:        "bash",
		Description: bashDescription,
		Required:    []string{"command"},
		Properties:  bashProperties,
	}, tools.RunBash)

	registry.Register(loop.ToolSpec{
		Name:        "read_file",
		Description: "Read a UTF-8 text file inside the workspace.",
		Required:    []string{"path"},
		Properties: map[string]any{
			"path":  map[string]any{"type": "string", "description": "Workspace-relative file path."},
			"limit": map[string]any{"type": "integer", "minimum": 1, "description": "Optional maximum number of lines."},
		},
	}, tools.RunReadFile)
	registry.Register(loop.ToolSpec{
		Name:        "write_file",
		Description: "Create or overwrite a file inside the workspace.",
		Required:    []string{"path", "content"},
		Properties: map[string]any{
			"path":    map[string]any{"type": "string", "description": "Workspace-relative file path."},
			"content": map[string]any{"type": "string", "description": "Full file content to write."},
		},
	}, tools.RunWriteFile)
	registry.Register(loop.ToolSpec{
		Name:        "edit_file",
		Description: "Replace one exact text occurrence in a workspace file.",
		Required:    []string{"path", "old_text", "new_text"},
		Properties: map[string]any{
			"path":     map[string]any{"type": "string", "description": "Workspace-relative file path."},
			"old_text": map[string]any{"type": "string", "description": "Exact text to replace."},
			"new_text": map[string]any{"type": "string", "description": "Replacement text."},
		},
	}, tools.RunEditFile)
	registry.Register(loop.ToolSpec{
		Name:        "glob",
		Description: "Find files by a glob pattern inside the workspace.",
		Required:    []string{"pattern"},
		Properties: map[string]any{
			"pattern": map[string]any{"type": "string", "description": "Glob pattern, for example **/*.go."},
		},
	}, tools.RunGlob)
}

func registerTodoTool(registry *loop.Registry, manager *todo.Manager) {
	registry.Register(loop.ToolSpec{
		Name:        "todo_write",
		Description: "Create and manage a task list for your current coding session. Use this before and during multi-step work.",
		Required:    []string{"todos"},
		Properties: map[string]any{
			"todos": map[string]any{
				"type":     "array",
				"maxItems": 20,
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"content": map[string]any{"type": "string", "minLength": 1},
						"status": map[string]any{
							"type": "string",
							"enum": []string{"pending", "in_progress", "completed"},
						},
					},
					"required": []string{"content", "status"},
				},
			},
		},
	}, manager.RunWrite)
}

func registerTaskTool(registry *loop.Registry, manager *subagent.Manager) {
	registry.Register(loop.ToolSpec{
		Name:        "task",
		Description: "Launch a subagent to handle a complex subtask. Returns only the final conclusion.",
		Required:    []string{"description"},
		Properties: map[string]any{
			"description": map[string]any{
				"type":        "string",
				"description": "Description of the subtask to delegate.",
			},
		},
	}, manager.RunTask)
}

func registerTaskSystemTools(registry *loop.Registry, manager *task.Manager) {
	// 任务节点必须先创建再建立依赖，因此 schema 明确约束运行时任务 ID。
	taskID := map[string]any{"type": "string", "pattern": `^task_[0-9a-f]{8}$`}
	registry.Register(loop.ToolSpec{
		Name: "create_task", Description: "Create a persistent pending task. Create all task nodes before adding dependencies.",
		Required: []string{"subject"}, Properties: map[string]any{
			"subject":     map[string]any{"type": "string", "minLength": 1},
			"description": map[string]any{"type": "string"},
		},
	}, manager.RunCreate)
	registry.Register(loop.ToolSpec{
		Name: "update_task", Description: "Add dependencies to an unowned pending task using IDs returned by create_task.",
		Required: []string{"task_id", "addBlockedBy"}, Properties: map[string]any{
			"task_id":      taskID,
			"addBlockedBy": map[string]any{"type": "array", "minItems": 1, "items": taskID},
		},
	}, manager.RunUpdate)
	registry.Register(loop.ToolSpec{Name: "list_tasks", Description: "List persistent tasks and their states."}, manager.RunList)
	registry.Register(loop.ToolSpec{
		Name: "get_task", Description: "Get one persistent task by ID.",
		Required: []string{"task_id"}, Properties: map[string]any{"task_id": taskID},
	}, manager.RunGet)
	registry.Register(loop.ToolSpec{
		Name: "claim_task", Description: "Claim a pending task after all dependencies are completed.",
		Required: []string{"task_id"}, Properties: map[string]any{"task_id": taskID},
	}, manager.RunClaim)
	registry.Register(loop.ToolSpec{
		Name: "complete_task", Description: "Complete a task owned by this agent and report newly unblocked tasks.",
		Required: []string{"task_id"}, Properties: map[string]any{"task_id": taskID},
	}, manager.RunComplete)
}

func registerCronTools(registry *loop.Registry, manager *agentruntime.CronScheduler) {
	registry.Register(loop.ToolSpec{
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
	registry.Register(loop.ToolSpec{Name: "list_crons", Description: "List scheduled cron jobs."}, manager.RunList)
	registry.Register(loop.ToolSpec{
		Name:        "cancel_cron",
		Description: "Cancel a scheduled cron job and any pending delivery.",
		Required:    []string{"job_id"},
		Properties: map[string]any{
			"job_id": map[string]any{"type": "string", "pattern": `^cron_[0-9a-f]{8}$`},
		},
	}, manager.RunCancel)
}

func registerSkillTool(registry *loop.Registry, manager *skill.Manager) {
	registry.Register(loop.ToolSpec{
		Name:        "load_skill",
		Description: "Load the full content of a skill by name.",
		Required:    []string{"name"},
		Properties: map[string]any{
			"name": map[string]any{"type": "string", "description": "Skill name"},
		},
	}, manager.RunLoad)
}

func registerCompactTool(registry *loop.Registry) {
	registry.Register(loop.ToolSpec{
		Name:        "compact",
		Description: "Summarize earlier conversation to free context space.",
		Properties: map[string]any{
			"focus": map[string]any{"type": "string", "description": "Optional focus for what the summary should preserve."},
		},
	}, func(context.Context, any) (string, error) {
		return "[compact is handled by the runner]", nil
	})
}
