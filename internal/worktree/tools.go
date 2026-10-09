package worktree

import "go-agent-harness/internal/tool"

// RegisterTool 只向 Lead 暴露创建工具；删除仍由宿主在检查任务租约和 Git 状态后决定。
func RegisterTool(registry *tool.Registry, manager *Manager) {
	taskID := map[string]any{"type": "string", "pattern": `^task_[0-9a-f]{8}$`}
	registry.Register(tool.Spec{Name: "create_worktree", Description: "Create and bind an optional Git Worktree to an unowned pending task.",
		Required: []string{"name", "task_id"}, Properties: map[string]any{
			"name":    map[string]any{"type": "string", "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`, "maxLength": 64},
			"task_id": taskID,
		}}, manager.RunCreate)
}
