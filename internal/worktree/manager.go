// Package worktree 管理与 Task 绑定的 Git Worktree 隔离工作区。
package worktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/workspace"
)

const (
	defaultDir  = ".worktrees"
	gitTimeout  = 30 * time.Second
	outputLimit = 16 * 1024
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Config 定义主仓库、Worktree 目录及运行时租约检查函数。
type Config struct {
	WorkDir     string
	WorktreeDir string
	Tasks       *task.Manager
	InUse       func(path string) bool
}

// Manager 负责 Git Worktree 与 Task 元数据之间的一致性。
type Manager struct {
	root    *workspace.Resolver
	dir     string
	tasks   *task.Manager
	inUse   func(string) bool
	initErr error
	mu      sync.Mutex
}

// New 创建 Worktree 管理器；目录必须位于主工作区内。
func New(cfg Config) *Manager {
	workDir := strings.TrimSpace(cfg.WorkDir)
	if workDir == "" {
		workDir = "."
	}
	dirName := strings.TrimSpace(cfg.WorktreeDir)
	if dirName == "" {
		dirName = defaultDir
	}
	resolver, err := workspace.New(workDir)
	if err != nil {
		return &Manager{tasks: cfg.Tasks, inUse: cfg.InUse, initErr: err}
	}
	dir, err := resolver.Resolve(dirName)
	return &Manager{root: resolver, dir: dir, tasks: cfg.Tasks, inUse: cfg.InUse, initErr: err}
}

// Create 从当前 HEAD 创建分支，并在 Git 成功后绑定 Task。
func (m *Manager) Create(ctx context.Context, name, taskID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	path, branch, err := m.prepare(ctx, name)
	if err != nil {
		return "", err
	}
	item, err := m.tasks.Get(taskID)
	if err != nil {
		return "", err
	}
	if item.Status != task.Pending || item.Owner != nil || item.Worktree != nil {
		return "", fmt.Errorf("task must be unowned, pending, and unbound: %s", taskID)
	}
	items, err := m.tasks.List()
	if err != nil {
		return "", err
	}
	for _, candidate := range items {
		if candidate.Worktree != nil && *candidate.Worktree == name {
			return "", fmt.Errorf("worktree is already bound to task %s: %s", candidate.ID, name)
		}
	}
	if _, err := os.Lstat(path); err == nil {
		return "", fmt.Errorf("worktree path already exists: %s", path)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	registered, err := m.worktrees(ctx)
	if err != nil {
		return "", err
	}
	if _, exists := registered[path]; exists {
		return "", fmt.Errorf("worktree is already registered: %s", name)
	}
	if output, err := m.git(ctx, "branch", "--list", "--format=%(refname)", branch); err != nil {
		return "", err
	} else if output != "" {
		return "", fmt.Errorf("branch already exists: %s", branch)
	}
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return "", err
	}

	// 先让 Git 原子创建分支和目录，再写 Task 绑定；失败时保留现场供人工恢复。
	if _, err := m.git(ctx, "worktree", "add", "-b", branch, path, "HEAD"); err != nil {
		logger.Error("[Worktree] Create %s for task %s: %v", name, taskID, err)
		return "", err
	}
	if _, err := m.tasks.BindWorktree(taskID, name); err != nil {
		logger.Warn("[Worktree] Git created %s but task %s binding failed: %v", name, taskID, err)
		return "", fmt.Errorf("worktree created but task binding failed: %w", err)
	}
	logger.Info("[Worktree] Created %s for task %s at %s", name, taskID, path)
	return path, nil
}

// Resolve 返回 Task 实际执行目录；已绑定但损坏的 Worktree 会直接报错，禁止退回主仓库。
func (m *Manager) Resolve(ctx context.Context, item task.Task) (*workspace.Resolver, error) {
	if err := m.ready(ctx); err != nil {
		return nil, err
	}
	if item.Worktree == nil {
		return m.root, nil
	}
	path, branch, err := m.location(ctx, *item.Worktree)
	if err != nil {
		return nil, err
	}
	registered, err := m.worktrees(ctx)
	if err != nil {
		return nil, err
	}
	entry, ok := registered[path]
	if !ok || entry.branch != "refs/heads/"+branch {
		return nil, fmt.Errorf("task %s has an unavailable worktree: %s", item.ID, *item.Worktree)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("path is not a directory")
		}
		return nil, fmt.Errorf("task %s worktree is invalid: %w", item.ID, err)
	}
	return workspace.New(path)
}

// Remove 仅移除已完成 Task 的 Worktree；分支会保留，便于后续审查或合并。
func (m *Manager) Remove(ctx context.Context, name string, discard bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	path, branch, err := m.prepare(ctx, name)
	if err != nil {
		return err
	}
	items, err := m.tasks.List()
	if err != nil {
		return err
	}
	var bound *task.Task
	for i := range items {
		if items[i].Worktree != nil && *items[i].Worktree == name {
			if bound != nil {
				return fmt.Errorf("worktree has multiple task bindings: %s", name)
			}
			bound = &items[i]
		}
	}
	if bound == nil {
		return fmt.Errorf("worktree is not bound to a task: %s", name)
	}
	if bound.Status != task.Completed {
		return fmt.Errorf("task must be completed before removing worktree: %s", bound.ID)
	}
	if m.inUse != nil && m.inUse(path) {
		return fmt.Errorf("worktree is still in use: %s", name)
	}
	registered, err := m.worktrees(ctx)
	if err != nil {
		return err
	}
	entry, ok := registered[path]
	if !ok || entry.branch != "refs/heads/"+branch {
		return fmt.Errorf("registered worktree does not match task binding: %s", name)
	}
	status, err := m.gitAt(ctx, path, "status", "--porcelain", "--ignored")
	if err != nil {
		return err
	}
	if status != "" && !discard {
		return fmt.Errorf("worktree has uncommitted or ignored files: %s", name)
	}
	args := []string{"worktree", "remove"}
	if discard {
		args = append(args, "--force")
	}
	args = append(args, path)
	if _, err := m.git(ctx, args...); err != nil {
		logger.Error("[Worktree] Remove %s for task %s: %v", name, bound.ID, err)
		return err
	}
	if _, err := m.tasks.ClearWorktree(bound.ID, name); err != nil {
		logger.Warn("[Worktree] Git removed %s but task %s unbinding failed: %v", name, bound.ID, err)
		return fmt.Errorf("worktree removed but task binding remains: %w", err)
	}
	logger.Info("[Worktree] Removed %s for task %s; branch %s retained", name, bound.ID, branch)
	return nil
}

func (m *Manager) prepare(ctx context.Context, name string) (string, string, error) {
	if err := m.ready(ctx); err != nil {
		return "", "", err
	}
	return m.location(ctx, name)
}

func (m *Manager) ready(ctx context.Context) error {
	if m == nil {
		return errors.New("worktree manager is nil")
	}
	if m.initErr != nil {
		return m.initErr
	}
	if m.tasks == nil {
		return errors.New("task manager is required")
	}
	top, err := m.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("workspace is not a git repository: %w", err)
	}
	resolved, err := workspace.New(top)
	if err != nil {
		return err
	}
	if resolved.Root() != m.root.Root() {
		return fmt.Errorf("workspace must be the git repository root: %s", m.root.Root())
	}
	return nil
}

func (m *Manager) location(ctx context.Context, name string) (string, string, error) {
	name = strings.TrimSpace(name)
	if !namePattern.MatchString(name) || name == "." || name == ".." || strings.Contains(name, "..") {
		return "", "", fmt.Errorf("invalid worktree name: %s", name)
	}
	path, err := m.root.Resolve(filepath.Join(m.dir, name))
	if err != nil {
		return "", "", err
	}
	if filepath.Dir(path) != m.dir {
		return "", "", fmt.Errorf("invalid worktree path: %s", name)
	}
	branch := "wt/" + name
	if _, err := m.git(ctx, "check-ref-format", "--branch", branch); err != nil {
		return "", "", fmt.Errorf("invalid worktree branch %s: %w", branch, err)
	}
	return path, branch, nil
}

type worktreeEntry struct {
	branch string
}

func (m *Manager) worktrees(ctx context.Context) (map[string]worktreeEntry, error) {
	output, err := m.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	entries := make(map[string]worktreeEntry)
	var path string
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			path = filepath.Clean(strings.TrimPrefix(line, "worktree "))
			entries[path] = worktreeEntry{}
		case path != "" && strings.HasPrefix(line, "branch "):
			entry := entries[path]
			entry.branch = strings.TrimPrefix(line, "branch ")
			entries[path] = entry
		case line == "":
			path = ""
		}
	}
	return entries, nil
}

func (m *Manager) git(ctx context.Context, args ...string) (string, error) {
	return m.gitAt(ctx, m.root.Root(), args...)
}

// gitAt 使用参数数组而非 Shell 拼接，并限制执行时长和日志输出体积。
func (m *Manager) gitAt(ctx context.Context, dir string, args ...string) (string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	logger.Debug("[Worktree] git -C %s %s", dir, strings.Join(args, " "))
	cmd := exec.CommandContext(commandCtx, "git", args...)
	cmd.Dir = dir
	output := &cappedBuffer{limit: outputLimit}
	cmd.Stdout = output
	cmd.Stderr = output
	err := cmd.Run()
	result := strings.TrimSpace(output.String())
	if commandCtx.Err() != nil {
		return result, fmt.Errorf("git %s timed out: %w", args[0], commandCtx.Err())
	}
	if err != nil {
		if result == "" {
			result = err.Error()
		}
		return result, fmt.Errorf("git %s: %s", args[0], result)
	}
	return result, nil
}

type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = b.Buffer.Write(data)
	}
	return original, nil
}
