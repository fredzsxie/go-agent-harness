// Package runtime 管理后台任务与 Cron Scheduler 的运行时生命周期。
package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go-agent-harness/internal/logger"
)

const commandTimeout = 120 * time.Second

type Executor func(context.Context, string) (string, error)

type task struct {
	id       string
	command  string
	status   string
	result   string
	complete func(string)
}

type BackgroundManager struct {
	mu      sync.Mutex
	tasks   map[string]*task
	ready   []string
	nextID  int
	execute Executor
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	closed  bool
	readyCh chan struct{}
}

// NewBackground 创建可跨多次 LLM 调用存活的后台任务管理器。
func NewBackground(execute Executor) *BackgroundManager {
	// 使用独立于单次 Runner.Run 的根 context，使任务能跨 LLM 回合继续执行；
	// 应用退出时再通过 Close 统一取消。
	ctx, cancel := context.WithCancel(context.Background())
	return &BackgroundManager{
		tasks:   make(map[string]*task),
		execute: execute,
		ctx:     ctx,
		cancel:  cancel,
		readyCh: make(chan struct{}, 1),
	}
}

// ShouldRunBackground 只接受 Bash 工具显式传入的布尔值 true，避免根据命令内容猜测执行方式。
func ShouldRunBackground(toolName string, input map[string]any) bool {
	background, ok := input["run_in_background"].(bool)
	return toolName == "bash" && ok && background
}

// Start 校验并登记命令，然后立即返回任务 ID，不等待命令执行完成。
func (m *BackgroundManager) Start(command string, onComplete ...func(string)) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", fmt.Errorf("bash command cannot be empty")
	}
	if m.execute == nil {
		return "", fmt.Errorf("background executor is not configured")
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return "", fmt.Errorf("background manager is closed")
	}
	m.nextID++
	id := fmt.Sprintf("bg_%04d", m.nextID)
	var complete func(string)
	if len(onComplete) > 0 {
		complete = onComplete[0]
	}
	m.tasks[id] = &task{id: id, command: command, status: "running", complete: complete}
	m.wg.Add(1)
	m.mu.Unlock()

	// 任务登记完成后再启动 goroutine，确保极快完成的命令也能找到自己的状态。
	logger.Info("[Background] started %s: %s", id, preview(command, 60))
	go m.run(id, command)
	return id, nil
}

// run 在后台执行单个命令，并把最终状态加入待收集队列。
func (m *BackgroundManager) run(id, command string) {
	defer m.wg.Done()
	// 每个后台命令独立限时，同时受 Manager.Close 的全局取消控制。
	ctx, cancel := context.WithTimeout(m.ctx, commandTimeout)
	defer cancel()

	output, err := m.execute(ctx, command)
	status := "completed"
	result := output
	if err != nil {
		status = "failed"
		if strings.TrimSpace(output) == "" {
			result = "Error: " + err.Error()
		} else {
			result = fmt.Sprintf("Error: %v\n%s", err, output)
		}
	} else if strings.TrimSpace(result) == "" {
		result = "(no output)"
	}
	if current := m.task(id); current != nil && current.complete != nil {
		current.complete(result)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.tasks[id]
	if !ok {
		return
	}
	current.status = status
	current.result = result
	// 只把 ID 放入完成队列；Collect 负责一次性读取并删除完整任务状态。
	m.ready = append(m.ready, id)
	select {
	case m.readyCh <- struct{}{}:
	default:
	}
	if status == "failed" {
		logger.Error("[Background] finished %s: %s", id, status)
	} else {
		logger.Info("[Background] finished %s: %s", id, status)
	}
}

func (m *BackgroundManager) task(id string) *task {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasks[id]
}

// Ready 在至少一个后台任务完成时通知运行时；多个结果可合并为一次唤醒。
func (m *BackgroundManager) Ready() <-chan struct{} {
	return m.readyCh
}

// HasReady 用于消除结果已被正在运行的 Agent 收集后留下的过期通知。
func (m *BackgroundManager) HasReady() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.ready) > 0
}

// HasRunning 区分“仍在执行”和“已经完成但尚未收集”，供 Goal Stop gate 延后评估。
func (m *BackgroundManager) HasRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, current := range m.tasks {
		if current.status == "running" {
			return true
		}
	}
	return false
}

// Collect 一次性取走已完成任务，并转换成不复用原 tool_use_id 的独立通知文本。
func (m *BackgroundManager) Collect() []string {
	m.mu.Lock()
	ready := m.ready
	m.ready = nil
	tasks := make([]task, 0, len(ready))
	for _, id := range ready {
		if current, ok := m.tasks[id]; ok {
			tasks = append(tasks, *current)
			delete(m.tasks, id)
		}
	}
	m.mu.Unlock()

	notifications := make([]string, 0, len(tasks))
	for _, current := range tasks {
		notifications = append(notifications, fmt.Sprintf(
			"<task_notification>\n  <task_id>%s</task_id>\n  <status>%s</status>\n  <command>%s</command>\n  <summary>%s</summary>\n</task_notification>",
			current.id, current.status, escape(current.command), escape(truncate(current.result, 500)),
		))
		// 只在结果真正交付给主循环时记录 collected，便于区分“已完成”和“已消费”。
		if current.status == "failed" {
			logger.Error("[Background] collected %s: %s", current.id, current.status)
		} else {
			logger.Info("[Background] collected %s: %s", current.id, current.status)
		}
	}
	return notifications
}

// Close 拒绝新任务、取消运行中的任务，并等待后台 goroutine 全部结束。
func (m *BackgroundManager) Close() {
	// 先阻止新任务并广播取消，再等待所有 goroutine 退出，避免应用结束后遗留任务。
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		m.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func preview(command string, limit int) string {
	// 将多行命令压成单行，避免一条生命周期日志占用多行终端输出。
	return truncate(strings.Join(strings.Fields(command), " "), limit)
}

func escape(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
}
