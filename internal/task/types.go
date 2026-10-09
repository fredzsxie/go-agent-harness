package task

import (
	"regexp"
	"sync"
)

// AgentOwner 是主 Agent 调用任务工具时使用的固定所有者标识。
const AgentOwner = "agent"

// Status 表示任务生命周期中的状态。
type Status string

const (
	Pending    Status = "pending"
	InProgress Status = "in_progress"
	Completed  Status = "completed"
)

var taskIDPattern = regexp.MustCompile(`^task_[0-9a-f]{8}$`)

// Task 是保存在 .tasks/{id}.json 中的单条任务记录。
type Task struct {
	ID          string   `json:"id"`
	Subject     string   `json:"subject"`
	Description string   `json:"description"`
	Status      Status   `json:"status"`
	Owner       *string  `json:"owner"`
	BlockedBy   []string `json:"blockedBy"`
	Worktree    *string  `json:"worktree,omitempty"`
}

// Config 定义任务存储所使用的工作区和目录。
type Config struct {
	WorkDir string
	TaskDir string
}

// Manager 负责任务记录的持久化和依赖图状态流转。
type Manager struct {
	dir     string
	initErr error
	mu      sync.Mutex
}
