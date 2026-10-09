package worktree

import "go-agent-harness/internal/task"

// TaskBindings 是 Worktree 所需的最小任务边界，不耦合任务工具或 Team 生命周期。
type TaskBindings interface {
	Get(string) (task.Task, error)
	List() ([]task.Task, error)
	BindWorktree(id, name string) (task.Task, error)
	ClearWorktree(id, name string) (task.Task, error)
}
