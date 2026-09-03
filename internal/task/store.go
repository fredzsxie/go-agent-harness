package task

import (
	"fmt"
	"os"
	"path/filepath"

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
