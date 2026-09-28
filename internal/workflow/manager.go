package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"go-agent-harness/internal/logger"
)

type ManagerConfig struct {
	Registry    *Registry
	Store       *Store
	Runner      AgentRunner
	AgentCap    int
	Concurrency int
}

type Manager struct {
	registry    *Registry
	store       *Store
	runner      AgentRunner
	agentCap    int
	concurrency int
}

func NewManager(config ManagerConfig) *Manager {
	return &Manager{
		registry: config.Registry, store: config.Store, runner: config.Runner,
		agentCap: config.AgentCap, concurrency: config.Concurrency,
	}
}

func (m *Manager) Names() []string {
	if m == nil || m.registry == nil {
		return nil
	}
	metadata := m.registry.List()
	names := make([]string, len(metadata))
	for index, item := range metadata {
		names[index] = item.Name
	}
	return names
}

// RunTool 是模型工具适配层；运行期失败会作为 completed tool_result 返回主 Agent。
func (m *Manager) RunTool(ctx context.Context, input any) (string, error) {
	parsed, err := ParseToolInput(input)
	if err != nil {
		return "", err
	}
	result, err := m.Run(ctx, parsed)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Run 在一个 tool_use 内完成启动、执行、持久化和通知闭环。
func (m *Manager) Run(ctx context.Context, input ToolInput) (Result, error) {
	if m == nil || m.registry == nil || m.store == nil || m.runner == nil {
		return Result{}, fmt.Errorf("workflow manager is not configured")
	}
	definition, ok := m.registry.Get(input.Name)
	if !ok {
		return Result{}, fmt.Errorf("unknown workflow %q", input.Name)
	}

	resuming := strings.TrimSpace(input.ResumeFromRunID) != ""
	runID := input.ResumeFromRunID
	var err error
	if resuming {
		if err = validateRunID(runID); err != nil {
			return Result{}, err
		}
	} else {
		runID, err = m.store.ReserveRun(definition.Metadata.Name)
		if err != nil {
			return Result{}, err
		}
	}
	release, err := m.store.LockRun(runID)
	if err != nil {
		return Result{}, err
	}
	defer release()
	return m.runLocked(ctx, definition, input, runID, resuming)
}

func (m *Manager) runLocked(ctx context.Context, definition Definition, input ToolInput, runID string, resuming bool) (Result, error) {
	args, err := m.resolveArgs(input, definition.Metadata.Name, runID, resuming)
	if err != nil {
		return Result{}, err
	}
	budget, err := workflowBudget(args)
	if err != nil {
		return Result{}, err
	}
	journal, err := m.store.OpenJournal(runID, resuming)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = journal.Close() }()

	task := Task{
		TaskID: "local_workflow_" + runID, TaskType: TaskType, RunID: runID,
		Workflow: definition.Metadata.Name, Status: StatusRunning, Progress: []Progress{},
	}
	launch := Launch{
		Status: "async_launched", TaskID: task.TaskID, TaskType: TaskType,
		RunID: runID, Workflow: definition.Metadata.Name,
	}
	if err := m.store.WriteSnapshot(Snapshot{RunID: runID, WorkflowName: definition.Metadata.Name, Args: args, Task: task}); err != nil {
		return Result{}, err
	}
	logger.Info("[Workflow] async_launched run=%s task=%s", runID, task.TaskID)
	logger.Info("[Workflow] task_started run=%s workflow=%s resume=%t", runID, definition.Metadata.Name, resuming)

	execution, err := NewExecution(ExecutionConfig{
		Registry: m.registry, Journal: journal, Runner: m.runner, Task: &task,
		TokenBudget: budget, AgentCap: m.agentCap, Concurrency: m.concurrency,
	})
	if err != nil {
		return Result{}, err
	}
	scriptArgs, err := cloneJSONMap(args)
	if err != nil {
		return Result{}, err
	}
	value, runErr := callScript(ctx, definition.Script, execution, scriptArgs)
	task = execution.SnapshotTask()
	if closeErr := journal.Close(); runErr == nil && closeErr != nil {
		runErr = closeErr
	}
	if runErr != nil {
		task.Status = StatusFailed
		value = map[string]any{"error": runErr.Error()}
		logger.Error("[Workflow] run failed run=%s: %v", runID, runErr)
	} else {
		task.Status = StatusCompleted
	}
	outputFile, err := m.store.WriteOutput(runID, value)
	if err != nil {
		return Result{}, err
	}
	task.OutputFile = outputFile
	if err := m.store.WriteSnapshot(Snapshot{RunID: runID, WorkflowName: definition.Metadata.Name, Args: args, Task: task}); err != nil {
		return Result{}, err
	}
	logger.Info("[Workflow] task_notification run=%s status=%s agents=%d tokens=%d output=%s", runID, task.Status, task.Usage.Agents, task.Usage.Tokens, outputFile)
	return Result{Launched: launch, Result: value, Task: task}, nil
}

// resolveArgs 在修改已完成 artifact 前验证 resume 的 Workflow 名称与原始参数。
func (m *Manager) resolveArgs(input ToolInput, workflowName, runID string, resuming bool) (map[string]any, error) {
	if !resuming {
		return cloneJSONMap(input.Args)
	}
	snapshot, err := m.store.ReadSnapshot(runID)
	if err != nil {
		return nil, err
	}
	if snapshot.WorkflowName != workflowName {
		return nil, fmt.Errorf("resume run ID does not match workflow %q", workflowName)
	}
	if !input.HasArgs {
		return cloneJSONMap(snapshot.Args)
	}
	equal, err := equalJSON(input.Args, snapshot.Args)
	if err != nil {
		return nil, err
	}
	if !equal {
		return nil, fmt.Errorf("resume args do not match the original run")
	}
	return cloneJSONMap(snapshot.Args)
}

func workflowBudget(args map[string]any) (int64, error) {
	raw, exists := args["budget"]
	if !exists {
		return 0, nil
	}
	var budget int64
	switch value := raw.(type) {
	case int:
		budget = int64(value)
	case int64:
		budget = value
	case float64:
		if math.Trunc(value) != value || value > math.MaxInt64 {
			return 0, fmt.Errorf("workflow args.budget must be a positive integer")
		}
		budget = int64(value)
	default:
		return 0, fmt.Errorf("workflow args.budget must be a positive integer")
	}
	if budget <= 0 {
		return 0, fmt.Errorf("workflow args.budget must be a positive integer")
	}
	return budget, nil
}

func cloneJSONMap(value map[string]any) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("workflow args must be JSON-safe: %w", err)
	}
	var cloned map[string]any
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}

func equalJSON(left, right map[string]any) (bool, error) {
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false, err
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftJSON, rightJSON), nil
}

func callScript(ctx context.Context, script Script, execution ExecutionContext, args map[string]any) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("workflow script panicked: %v", recovered)
		}
	}()
	return script(ctx, execution, args)
}
