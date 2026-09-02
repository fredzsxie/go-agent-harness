package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"go-agent-harness/internal/logger"
)

// Load 在进程启动时恢复 durable job；待投递任务同时回到内存队列。
func (m *CronScheduler) Load() error {
	if m.initErr != nil {
		return m.initErr
	}
	data, err := os.ReadFile(m.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var records []json.RawMessage
	if err := json.Unmarshal(data, &records); err != nil {
		return fmt.Errorf("could not load %s: %w", filepath.Base(m.path), err)
	}
	if records == nil {
		return fmt.Errorf("could not load %s: expected a JSON list", filepath.Base(m.path))
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	loaded := 0
	for _, record := range records {
		var job CronJob
		decoder := json.NewDecoder(bytes.NewReader(record))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&job); err != nil {
			logger.Error("[cron] skipped invalid saved job: %v", err)
			continue
		}
		if err := validateCronJob(job); err != nil {
			logger.Error("[cron] skipped invalid saved job: %v", err)
			continue
		}
		if _, exists := m.jobs[job.ID]; exists {
			logger.Error("[cron] skipped duplicate saved job: %s", job.ID)
			continue
		}
		copyJob := job
		m.jobs[job.ID] = &copyJob
		if job.PendingDelivery {
			m.queue = append(m.queue, job.ID)
		}
		loaded++
	}
	if loaded > 0 {
		logger.Info("[cron] loaded %d durable job(s)", loaded)
	}
	return nil
}

// saveLocked 使用临时文件和 rename 原子保存 durable job。
func (m *CronScheduler) saveLocked() error {
	jobs := make([]CronJob, 0, len(m.jobs))
	for _, job := range m.jobs {
		if job.Durable {
			jobs = append(jobs, *job)
		}
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(m.path), ".scheduled_tasks.*.tmp")
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
	return os.Rename(temporaryPath, m.path)
}
