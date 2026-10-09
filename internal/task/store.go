package task

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"

	"go-agent-harness/internal/logger"
)

// lockStore 组合进程内 Mutex 与文件锁，使多个 Teammate 或 Harness 进程串行修改 Task。
func (m *Manager) lockStore() (func(), error) {
	if err := m.ready(true); err != nil {
		return nil, err
	}
	m.mu.Lock()
	lock, err := os.OpenFile(filepath.Join(m.dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		_ = lock.Close()
		m.mu.Unlock()
		return nil, err
	}
	return func() {
		if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
			logger.Error("[Task] unlock store: %v", err)
		}
		if err := lock.Close(); err != nil {
			logger.Error("[Task] close store lock: %v", err)
		}
		m.mu.Unlock()
	}, nil
}

// save 先写临时文件再原子替换，读者不会观察到半条 JSON 记录。
func (m *Manager) save(task Task) error {
	data, err := encode(task)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(m.dir, "."+task.ID+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	err = temporary.Chmod(0o644)
	if err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, m.path(task.ID)); err != nil {
		return fmt.Errorf("replace task %s: %w", task.ID, err)
	}
	return nil
}

// Get 读取并校验指定任务，拒绝非法 ID 和损坏的任务记录。
func (m *Manager) Get(id string) (Task, error) {
	if err := m.ready(false); err != nil {
		return Task{}, err
	}
	if !taskIDPattern.MatchString(id) {
		return Task{}, fmt.Errorf("invalid task id: %s", id)
	}
	data, err := os.ReadFile(m.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return Task{}, fmt.Errorf("task not found: %s", id)
		}
		return Task{}, err
	}
	var task Task
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&task); err != nil {
		return Task{}, fmt.Errorf("read task %s: %w", id, err)
	}
	if task.ID != id || strings.TrimSpace(task.Subject) == "" || !validStatus(task.Status) || !validOwnership(task) {
		return Task{}, fmt.Errorf("invalid task record: %s", id)
	}
	if task.BlockedBy == nil {
		task.BlockedBy = []string{}
	}
	return task, nil
}

// List 按任务 ID 排序返回全部持久化任务。
func (m *Manager) List() ([]Task, error) {
	if err := m.ready(false); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.dir)
	if os.IsNotExist(err) {
		return []Task{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".json")
		if !entry.IsDir() && strings.HasSuffix(name, ".json") && taskIDPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	tasks := make([]Task, 0, len(ids))
	for _, id := range ids {
		task, err := m.Get(id)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (m *Manager) ready(create bool) error {
	if m.initErr != nil {
		return m.initErr
	}
	if create {
		return os.MkdirAll(m.dir, 0o755)
	}
	return nil
}

func (m *Manager) path(id string) string { return filepath.Join(m.dir, id+".json") }

// newID 生成 task_ 前缀加 8 位十六进制字符的运行时 ID。
func newID() (string, error) {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return "task_" + hex.EncodeToString(buffer), nil
}

func encode(task Task) ([]byte, error) {
	data, err := json.MarshalIndent(task, "", "  ")
	return append(data, '\n'), err
}

func validStatus(status Status) bool {
	return status == Pending || status == InProgress || status == Completed
}

func validOwnership(task Task) bool {
	if task.Status == Pending {
		return task.Owner == nil
	}
	return task.Owner != nil && strings.TrimSpace(*task.Owner) != ""
}
