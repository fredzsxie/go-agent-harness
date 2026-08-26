package app

import (
	"context"

	"go-agent-harness/internal/skill"
	"go-agent-harness/internal/subagent"
	"go-agent-harness/internal/todo"
	"go-agent-harness/loop"
	"go-agent-harness/tools"
)

func newDefaultRegistry() *loop.Registry {
	registry := loop.NewRegistry()
	registerBaseTools(registry)
	return registry
}

func newSubagentRegistry() *loop.Registry {
	registry := loop.NewRegistry()
	registerBaseTools(registry)
	return registry
}

func registerBaseTools(registry *loop.Registry) {
	registry.Register(loop.ToolSpec{
		Name:        "bash",
		Description: "Run a shell command in the current workspace. Prefer read_file/glob for inspection when possible.",
		Required:    []string{"command"},
		Properties: map[string]any{
			"command": map[string]any{"type": "string", "description": "Shell command to execute."},
		},
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
