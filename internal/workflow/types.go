// Package workflow 提供由 Host 注册、可恢复执行的 Workflow Runtime。
package workflow

import "context"

const TaskType = "local_workflow"

type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// Metadata 描述由 Host 注册的可信 Workflow，不接受模型动态覆盖。
type Metadata struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Phases      []string `json:"phases,omitempty"`
}

type AgentOptions struct {
	Schema map[string]any
	Label  string
	Phase  string
}

type Step func(context.Context) (any, error)
type Stage func(context.Context, any, any, int) (any, error)

// ExecutionContext 是可信脚本可使用的最小编排接口，不暴露文件或 Shell 能力。
type ExecutionContext interface {
	Agent(context.Context, string, AgentOptions) (any, error)
	Parallel(context.Context, []Step) ([]any, error)
	Pipeline(context.Context, []any, ...Stage) ([]any, error)
	Phase(string)
	Log(string)
	Workflow(context.Context, string, map[string]any) (any, error)
}

type Script func(context.Context, ExecutionContext, map[string]any) (any, error)

type Definition struct {
	Metadata Metadata
	Script   Script
}

// ToolInput 是模型调用 workflow 工具时唯一允许提供的参数。
type ToolInput struct {
	Name            string
	Args            map[string]any
	HasArgs         bool
	ResumeFromRunID string
}

type Usage struct {
	Agents int   `json:"agents"`
	Tokens int64 `json:"tokens"`
}

type Progress struct {
	Type    string         `json:"type"`
	Details map[string]any `json:"details,omitempty"`
}

type Task struct {
	TaskID     string     `json:"taskId"`
	TaskType   string     `json:"taskType"`
	RunID      string     `json:"runId"`
	Workflow   string     `json:"workflowName"`
	Status     Status     `json:"status"`
	Usage      Usage      `json:"usage"`
	Progress   []Progress `json:"progress"`
	OutputFile string     `json:"outputFile,omitempty"`
}

type Launch struct {
	Status   string `json:"status"`
	TaskID   string `json:"taskId"`
	TaskType string `json:"taskType"`
	RunID    string `json:"runId"`
	Workflow string `json:"workflowName"`
}

type Result struct {
	Launched Launch `json:"launched"`
	Result   any    `json:"result"`
	Task     Task   `json:"task"`
}
