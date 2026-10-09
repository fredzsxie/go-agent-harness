package team

import (
	"context"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/workspace"
)

// TaskBoard 只描述 Team 消费的任务操作；文件持久化和工具参数解析仍由 task 包负责。
// 接口放在使用方，避免 Runtime 依赖具体存储或要求存储实现 Team 专用行为。
type TaskBoard interface {
	Get(string) (task.Task, error)
	List() ([]task.Task, error)
	Claim(id, owner string) (task.Task, error)
	Complete(id, owner string) (task.Task, []task.Task, error)
	ReleaseOwner(owner string) (bool, error)
}

// WorkspaceProvider 为已认领任务提供执行目录；无 worktree 与绑定失效由实现决定。
type WorkspaceProvider interface {
	Resolve(context.Context, task.Task) (*workspace.Resolver, error)
}
