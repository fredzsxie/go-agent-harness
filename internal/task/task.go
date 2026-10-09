// Package task 实现 s10 的持久化任务图，与 s05 的临时 Todo 清单相互独立。
// 先 Create 全部节点，再用返回的 ID 建立依赖；Claim 与 Complete 负责验证依赖和所有权。
// 本文件维护状态流转，store.go 负责锁和文件操作，tools.go 负责模型输入与展示。
package task

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/workspace"
)

// New 创建任务管理器，并确保任务目录不能逃逸工作区。
func New(cfg Config) *Manager {
	workDir := strings.TrimSpace(cfg.WorkDir)
	if workDir == "" {
		workDir = "."
	}
	taskDir := strings.TrimSpace(cfg.TaskDir)
	if taskDir == "" {
		taskDir = ".tasks"
	}
	resolver, err := workspace.New(workDir)
	if err != nil {
		return &Manager{initErr: err}
	}
	dir, err := resolver.Resolve(taskDir)
	return &Manager{dir: dir, initErr: err}
}

// Create 使用随机 ID 独占创建任务，避免并发创建时覆盖已有记录。
func (m *Manager) Create(subject, description string) (Task, error) {
	unlock, err := m.lockStore()
	if err != nil {
		return Task{}, err
	}
	defer unlock()
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return Task{}, errors.New("subject is required")
	}
	for range 100 {
		id, err := newID()
		if err != nil {
			return Task{}, err
		}
		task := Task{ID: id, Subject: subject, Description: description, Status: Pending, BlockedBy: []string{}}
		if _, err := os.Stat(m.path(id)); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return Task{}, err
		}
		err = m.save(task)
		if err == nil {
			logger.Info("[Task] Created %s: %s", task.ID, task.Subject)
		}
		return task, err
	}
	return Task{}, errors.New("could not allocate a unique task id")
}

// AddBlockedBy 为待处理且未认领的任务添加依赖。
// 所有依赖会先完成存在性和环路校验，再一次性写入，避免部分更新。
func (m *Manager) AddBlockedBy(id string, dependencies []string) (Task, error) {
	unlock, err := m.lockStore()
	if err != nil {
		return Task{}, err
	}
	defer unlock()

	task, err := m.Get(id)
	if err != nil {
		return Task{}, err
	}
	if task.Status != Pending || task.Owner != nil {
		return Task{}, fmt.Errorf("only an unowned pending task can be updated: %s", id)
	}
	if len(dependencies) == 0 {
		return Task{}, errors.New("addBlockedBy requires at least one task id")
	}
	seen := make(map[string]bool, len(task.BlockedBy)+len(dependencies))
	for _, dependency := range task.BlockedBy {
		seen[dependency] = true
	}
	toAdd := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		if dependency == id {
			return Task{}, errors.New("a task cannot depend on itself")
		}
		if _, err := m.Get(dependency); err != nil {
			return Task{}, err
		}
		cyclic, err := m.dependsOn(dependency, id)
		if err != nil {
			return Task{}, err
		}
		if cyclic {
			return Task{}, fmt.Errorf("dependency would create a cycle: %s -> %s", id, dependency)
		}
		if !seen[dependency] {
			seen[dependency] = true
			toAdd = append(toAdd, dependency)
		}
	}
	task.BlockedBy = append(task.BlockedBy, toAdd...)
	if err := m.save(task); err != nil {
		return Task{}, err
	}
	logger.Info("[Task] Updated %s blockedBy: %s", task.ID, strings.Join(task.BlockedBy, ", "))
	return task, nil
}

// Claim 在全部前置任务完成后，将任务从 pending 推进到 in_progress。
func (m *Manager) Claim(id, owner string) (Task, error) {
	unlock, err := m.lockStore()
	if err != nil {
		return Task{}, err
	}
	defer unlock()
	return m.claimLocked(id, owner)
}

// ClaimNext 原子认领按 ID 排序后的首个 ready Task，避免扫描和认领之间发生竞争。
func (m *Manager) ClaimNext(owner string) (Task, bool, error) {
	unlock, err := m.lockStore()
	if err != nil {
		return Task{}, false, err
	}
	defer unlock()

	owner = strings.TrimSpace(owner)
	if owner == "" {
		return Task{}, false, errors.New("owner is required")
	}
	if current, err := m.currentLocked(owner); err != nil {
		return Task{}, false, err
	} else if current != nil {
		return Task{}, false, fmt.Errorf("owner already has an in-progress task: %s", current.ID)
	}
	tasks, err := m.List()
	if err != nil {
		return Task{}, false, err
	}
	for _, candidate := range tasks {
		if candidate.Status != Pending || candidate.Owner != nil {
			continue
		}
		ready, err := m.canStart(candidate)
		if err != nil {
			return Task{}, false, err
		}
		if !ready {
			continue
		}
		claimed, err := m.claimLocked(candidate.ID, owner)
		return claimed, err == nil, err
	}
	return Task{}, false, nil
}

func (m *Manager) claimLocked(id, owner string) (Task, error) {
	task, err := m.Get(id)
	if err != nil {
		return Task{}, err
	}
	if task.Status != Pending {
		return Task{}, fmt.Errorf("task is not pending: %s", id)
	}
	ready, err := m.canStart(task)
	if err != nil {
		return Task{}, err
	}
	if !ready {
		return Task{}, fmt.Errorf("task is blocked: %s", id)
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return Task{}, errors.New("owner is required")
	}
	if current, err := m.currentLocked(owner); err != nil {
		return Task{}, err
	} else if current != nil {
		return Task{}, fmt.Errorf("owner already has an in-progress task: %s", current.ID)
	}
	task.Status = InProgress
	task.Owner = &owner
	if err := m.save(task); err != nil {
		return Task{}, err
	}
	logger.Info("[Task] Claimed %s: %s (owner: %s)", task.ID, task.Subject, owner)
	return task, nil
}

// Complete 仅允许任务所有者完成任务，并返回本次状态变化新解锁的下游任务。
func (m *Manager) Complete(id, owner string) (Task, []Task, error) {
	unlock, err := m.lockStore()
	if err != nil {
		return Task{}, nil, err
	}
	defer unlock()

	task, err := m.Get(id)
	if err != nil {
		return Task{}, nil, err
	}
	if task.Status != InProgress || task.Owner == nil || *task.Owner != owner {
		return Task{}, nil, fmt.Errorf("task is not owned by %s: %s", owner, id)
	}
	tasks, err := m.List()
	if err != nil {
		return Task{}, nil, err
	}
	readyBefore := make(map[string]bool)
	for _, candidate := range tasks {
		if candidate.Status == Pending {
			ready, _ := m.canStart(candidate)
			readyBefore[candidate.ID] = ready
		}
	}
	task.Status = Completed
	if err := m.save(task); err != nil {
		return Task{}, nil, err
	}
	unblocked := make([]Task, 0)
	for _, candidate := range tasks {
		if candidate.Status != Pending || readyBefore[candidate.ID] {
			continue
		}
		ready, _ := m.canStart(candidate)
		if ready {
			unblocked = append(unblocked, candidate)
		}
	}
	logger.Info("[Task] Completed %s: %s", task.ID, task.Subject)
	if len(unblocked) > 0 {
		subjects := make([]string, len(unblocked))
		for i := range unblocked {
			subjects[i] = unblocked[i].Subject
		}
		logger.Info("[Task] Unblocked: %s", strings.Join(subjects, ", "))
	}
	return task, unblocked, nil
}

// Current 返回 Owner 当前认领的 Task；同一 Owner 最多只能有一个进行中任务。
func (m *Manager) Current(owner string) (*Task, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return nil, errors.New("owner is required")
	}
	return m.currentLocked(owner)
}

func (m *Manager) currentLocked(owner string) (*Task, error) {
	tasks, err := m.List()
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		if tasks[i].Status == InProgress && tasks[i].Owner != nil && *tasks[i].Owner == owner {
			copyTask := tasks[i]
			return &copyTask, nil
		}
	}
	return nil, nil
}

// ReleaseOwner 将异常退出 Teammate 的进行中任务恢复为 pending。
func (m *Manager) ReleaseOwner(owner string) (bool, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return false, errors.New("owner is required")
	}
	unlock, err := m.lockStore()
	if err != nil {
		return false, err
	}
	defer unlock()

	task, err := m.currentLocked(owner)
	if err != nil || task == nil {
		return false, err
	}
	task.Status = Pending
	task.Owner = nil
	if err := m.save(*task); err != nil {
		return false, err
	}
	logger.Warn("[Task] Released %s from owner %s", task.ID, owner)
	return true, nil
}

// BindWorktree 只允许为未认领的 pending Task 绑定唯一 Worktree 名称。
func (m *Manager) BindWorktree(id, name string) (Task, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Task{}, errors.New("worktree name is required")
	}
	unlock, err := m.lockStore()
	if err != nil {
		return Task{}, err
	}
	defer unlock()

	task, err := m.Get(id)
	if err != nil {
		return Task{}, err
	}
	if task.Status != Pending || task.Owner != nil {
		return Task{}, fmt.Errorf("task must be pending and unowned: %s", id)
	}
	if task.Worktree != nil {
		return Task{}, fmt.Errorf("task already uses worktree %s: %s", *task.Worktree, id)
	}
	tasks, err := m.List()
	if err != nil {
		return Task{}, err
	}
	for _, candidate := range tasks {
		if candidate.Worktree != nil && *candidate.Worktree == name {
			return Task{}, fmt.Errorf("worktree is already bound to task %s: %s", candidate.ID, name)
		}
	}
	task.Worktree = &name
	if err := m.save(task); err != nil {
		return Task{}, err
	}
	logger.Info("[Task] Bound %s to worktree %s", task.ID, name)
	return task, nil
}

// ClearWorktree 只清理已完成 Task 的匹配绑定，避免移除仍可能执行的工作目录。
func (m *Manager) ClearWorktree(id, name string) (Task, error) {
	unlock, err := m.lockStore()
	if err != nil {
		return Task{}, err
	}
	defer unlock()

	task, err := m.Get(id)
	if err != nil {
		return Task{}, err
	}
	if task.Status != Completed {
		return Task{}, fmt.Errorf("task must be completed before clearing worktree: %s", id)
	}
	if task.Worktree == nil || *task.Worktree != name {
		return Task{}, fmt.Errorf("task is not bound to worktree %s: %s", name, id)
	}
	task.Worktree = nil
	if err := m.save(task); err != nil {
		return Task{}, err
	}
	logger.Info("[Task] Cleared worktree %s from %s", name, task.ID)
	return task, nil
}

// canStart 仅在所有直接依赖均为 completed 时返回 true。
func (m *Manager) canStart(task Task) (bool, error) {
	for _, id := range task.BlockedBy {
		dependency, err := m.Get(id)
		if err != nil {
			return false, err
		}
		if dependency.Status != Completed {
			return false, nil
		}
	}
	return true, nil
}

// dependsOn 迭代遍历依赖图，判断 start 是否直接或间接依赖 target。
func (m *Manager) dependsOn(start, target string) (bool, error) {
	seen := make(map[string]bool)
	stack := []string{start}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == target {
			return true, nil
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		task, err := m.Get(id)
		if err != nil {
			return false, err
		}
		stack = append(stack, task.BlockedBy...)
	}
	return false, nil
}

// ready 延迟返回初始化错误，并按需创建任务目录。
