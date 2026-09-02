package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go-agent-harness/internal/logging"
	"go-agent-harness/internal/workspace"
)

var cronIDPattern = regexp.MustCompile(`^cron_[0-9a-f]{8}$`)

// CronJob 保存一条定时提示及其投递状态。
type CronJob struct {
	ID              string  `json:"id"`
	Cron            string  `json:"cron"`
	Prompt          string  `json:"prompt"`
	Recurring       bool    `json:"recurring"`
	Durable         bool    `json:"durable"`
	PendingDelivery bool    `json:"pending_delivery"`
	LastFired       *string `json:"last_fired"`
}

// CronConfig 指定工作区与 durable job 的存储路径。
type CronConfig struct {
	WorkDir string
	Path    string
}

// CronScheduler 管理 Cron 定义、持久化状态和待投递队列，不负责调用 Agent。
type CronScheduler struct {
	mu      sync.Mutex
	jobs    map[string]*CronJob
	queue   []string
	path    string
	initErr error
}

// NewCron 创建状态彼此独立的 Cron Scheduler，并限制持久化文件不能逃逸工作区。
func NewCron(cfg CronConfig) *CronScheduler {
	workDir := strings.TrimSpace(cfg.WorkDir)
	if workDir == "" {
		workDir = "."
	}
	path := strings.TrimSpace(cfg.Path)
	if path == "" {
		path = ".scheduled_tasks.json"
	}
	resolver, err := workspace.New(workDir)
	if err == nil {
		path, err = resolver.Resolve(path)
	}
	return &CronScheduler{jobs: make(map[string]*CronJob), path: path, initErr: err}
}

// ValidateCron 校验五段 cron 表达式，支持 *、*/N、N、N-M 和逗号列表。
func ValidateCron(expression string) error {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return fmt.Errorf("expected 5 fields, got %d", len(fields))
	}
	rules := []struct {
		name     string
		min, max int
	}{
		{"minute", 0, 59},
		{"hour", 0, 23},
		{"day-of-month", 1, 31},
		{"month", 1, 12},
		{"day-of-week", 0, 6},
	}
	for i, field := range fields {
		if err := validateCronField(field, rules[i].min, rules[i].max); err != nil {
			return fmt.Errorf("%s: %w", rules[i].name, err)
		}
	}
	return nil
}

func validateCronField(field string, min, max int) error {
	if field == "*" {
		return nil
	}
	if strings.HasPrefix(field, "*/") {
		step, err := strconv.Atoi(strings.TrimPrefix(field, "*/"))
		if err != nil || step <= 0 {
			return fmt.Errorf("invalid step: %s", field)
		}
		return nil
	}
	if strings.Contains(field, ",") {
		for _, part := range strings.Split(field, ",") {
			if err := validateCronField(strings.TrimSpace(part), min, max); err != nil {
				return err
			}
		}
		return nil
	}
	if strings.Contains(field, "-") {
		parts := strings.SplitN(field, "-", 2)
		start, startErr := strconv.Atoi(parts[0])
		end, endErr := strconv.Atoi(parts[1])
		if startErr != nil || endErr != nil {
			return fmt.Errorf("invalid range: %s", field)
		}
		if start > end {
			return fmt.Errorf("range start is greater than end: %s", field)
		}
		if start < min || end > max {
			return fmt.Errorf("range %s is outside [%d-%d]", field, min, max)
		}
		return nil
	}
	value, err := strconv.Atoi(field)
	if err != nil {
		return fmt.Errorf("invalid field: %s", field)
	}
	if value < min || value > max {
		return fmt.Errorf("value %d is outside [%d-%d]", value, min, max)
	}
	return nil
}

// CronMatches 使用本地时间判断表达式是否命中；日期与星期同时受限时采用 cron 的 OR 语义。
func CronMatches(expression string, moment time.Time) bool {
	if ValidateCron(expression) != nil {
		return false
	}
	fields := strings.Fields(expression)
	if !cronFieldMatches(fields[0], moment.Minute()) ||
		!cronFieldMatches(fields[1], moment.Hour()) ||
		!cronFieldMatches(fields[3], int(moment.Month())) {
		return false
	}
	dayMatches := cronFieldMatches(fields[2], moment.Day())
	weekdayMatches := cronFieldMatches(fields[4], int(moment.Weekday()))
	switch {
	case fields[2] == "*" && fields[4] == "*":
		return true
	case fields[2] == "*":
		return weekdayMatches
	case fields[4] == "*":
		return dayMatches
	default:
		return dayMatches || weekdayMatches
	}
}

func cronFieldMatches(field string, value int) bool {
	if field == "*" {
		return true
	}
	if strings.HasPrefix(field, "*/") {
		step, _ := strconv.Atoi(strings.TrimPrefix(field, "*/"))
		return value%step == 0
	}
	if strings.Contains(field, ",") {
		for _, part := range strings.Split(field, ",") {
			if cronFieldMatches(strings.TrimSpace(part), value) {
				return true
			}
		}
		return false
	}
	if strings.Contains(field, "-") {
		parts := strings.SplitN(field, "-", 2)
		start, _ := strconv.Atoi(parts[0])
		end, _ := strconv.Atoi(parts[1])
		return value >= start && value <= end
	}
	want, _ := strconv.Atoi(field)
	return value == want
}

func validateCronJob(job CronJob) error {
	if !cronIDPattern.MatchString(job.ID) {
		return errors.New("invalid job ID")
	}
	if err := ValidateCron(job.Cron); err != nil {
		return err
	}
	if strings.TrimSpace(job.Prompt) == "" {
		return errors.New("prompt cannot be empty")
	}
	return nil
}

// Schedule 先持久化 durable job，再向调用方暴露成功结果；失败时回滚内存状态。
func (m *CronScheduler) Schedule(expression, prompt string, recurring, durable bool) (CronJob, error) {
	if m.initErr != nil {
		return CronJob{}, m.initErr
	}
	if err := ValidateCron(expression); err != nil {
		return CronJob{}, err
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return CronJob{}, errors.New("prompt cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	id, err := m.newIDLocked()
	if err != nil {
		return CronJob{}, err
	}
	job := &CronJob{ID: id, Cron: strings.Join(strings.Fields(expression), " "), Prompt: prompt, Recurring: recurring, Durable: durable}
	m.jobs[id] = job
	if durable {
		if err := m.saveLocked(); err != nil {
			delete(m.jobs, id)
			return CronJob{}, err
		}
	}
	logging.Printf("[cron] scheduled %s: %s -> %s", id, job.Cron, preview(prompt, 60))
	return *job, nil
}

func (m *CronScheduler) newIDLocked() (string, error) {
	for range 100 {
		var raw [4]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		id := "cron_" + hex.EncodeToString(raw[:])
		if _, exists := m.jobs[id]; !exists {
			return id, nil
		}
	}
	return "", errors.New("could not allocate a cron job ID")
}

// List 返回按 ID 排序的任务副本，避免调用方修改内部状态。
func (m *CronScheduler) List() []CronJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := make([]CronJob, 0, len(m.jobs))
	for _, job := range m.jobs {
		jobs = append(jobs, copyCronJob(job))
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	return jobs
}

// Cancel 同时移除任务定义与尚未消费的队列项，持久化失败时恢复两者。
func (m *CronScheduler) Cancel(id string) error {
	if m.initErr != nil {
		return m.initErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	previousQueue := append([]string(nil), m.queue...)
	delete(m.jobs, id)
	m.removeQueuedLocked(id)
	if job.Durable {
		if err := m.saveLocked(); err != nil {
			m.jobs[id] = job
			m.queue = previousQueue
			return err
		}
	}
	logging.Printf("[cron] cancelled %s", id)
	return nil
}

// Poll 把当前分钟到期的 job 标为 pending 后入队，同一分钟不会重复触发。
func (m *CronScheduler) Poll(moment time.Time) {
	marker := moment.Format("2006-01-02 15:04")
	m.mu.Lock()
	ids := make([]string, 0, len(m.jobs))
	for id := range m.jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		job := m.jobs[id]
		if job.PendingDelivery || job.LastFired != nil && *job.LastFired == marker || !CronMatches(job.Cron, moment) {
			continue
		}
		oldLast := job.LastFired
		job.PendingDelivery = true
		job.LastFired = &marker
		if job.Durable {
			if err := m.saveLocked(); err != nil {
				job.PendingDelivery = false
				job.LastFired = oldLast
				logging.Printf("[cron] could not enqueue %s: %v", id, err)
				continue
			}
		}
		m.queue = append(m.queue, id)
		logging.Printf("[cron] due %s: %s", id, preview(job.Prompt, 60))
	}
	m.mu.Unlock()
}

func (m *CronScheduler) HasPending() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue) > 0
}

// Consume 只转移队列所有权；收到模型确认后再由 Acknowledge 更新 job。
func (m *CronScheduler) Consume() []CronJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := make([]CronJob, 0, len(m.queue))
	for _, id := range m.queue {
		if job, ok := m.jobs[id]; ok {
			jobs = append(jobs, copyCronJob(job))
		}
	}
	m.queue = nil
	return jobs
}

// Acknowledge 清除 recurring job 的 pending 状态，并删除已投递的一次性 job。
func (m *CronScheduler) Acknowledge(delivered []CronJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	previous := cloneCronJobs(m.jobs)
	needsSave := false
	for _, item := range delivered {
		job, ok := m.jobs[item.ID]
		if !ok {
			continue
		}
		needsSave = needsSave || job.Durable
		if job.Recurring {
			job.PendingDelivery = false
		} else {
			delete(m.jobs, job.ID)
		}
	}
	if needsSave {
		if err := m.saveLocked(); err != nil {
			m.jobs = previous
			m.restoreLocked(delivered)
			return err
		}
	}
	return nil
}

// Restore 在 Agent 调用失败时把未确认任务放回队列，保证至少投递一次。
func (m *CronScheduler) Restore(delivered []CronJob) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restoreLocked(delivered)
}

func (m *CronScheduler) restoreLocked(delivered []CronJob) {
	queued := make(map[string]bool, len(m.queue))
	for _, id := range m.queue {
		queued[id] = true
	}
	for _, item := range delivered {
		if job, ok := m.jobs[item.ID]; ok {
			job.PendingDelivery = true
			if !queued[item.ID] {
				m.queue = append(m.queue, item.ID)
				queued[item.ID] = true
			}
		}
	}
}

func cloneCronJobs(source map[string]*CronJob) map[string]*CronJob {
	cloned := make(map[string]*CronJob, len(source))
	for id, job := range source {
		copyJob := copyCronJob(job)
		cloned[id] = &copyJob
	}
	return cloned
}

func copyCronJob(job *CronJob) CronJob {
	copyJob := *job
	if job.LastFired != nil {
		marker := *job.LastFired
		copyJob.LastFired = &marker
	}
	return copyJob
}

func (m *CronScheduler) removeQueuedLocked(id string) {
	kept := m.queue[:0]
	for _, queued := range m.queue {
		if queued != id {
			kept = append(kept, queued)
		}
	}
	m.queue = kept
}

func (m *CronScheduler) RunSchedule(_ context.Context, input any) (string, error) {
	payload, _ := input.(map[string]any)
	expression, _ := payload["cron"].(string)
	prompt, _ := payload["prompt"].(string)
	recurring, err := optionalBool(payload, "recurring", true)
	if err != nil {
		return "", err
	}
	durable, err := optionalBool(payload, "durable", true)
	if err != nil {
		return "", err
	}
	job, err := m.Schedule(expression, prompt, recurring, durable)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Scheduled %s: %s -> %s", job.ID, job.Cron, job.Prompt), nil
}

func (m *CronScheduler) RunList(context.Context, any) (string, error) {
	jobs := m.List()
	if len(jobs) == 0 {
		return "No cron jobs.", nil
	}
	lines := make([]string, 0, len(jobs))
	for _, job := range jobs {
		frequency := "one-shot"
		if job.Recurring {
			frequency = "recurring"
		}
		storage := "session"
		if job.Durable {
			storage = "durable"
		}
		lines = append(lines, fmt.Sprintf("%s: %s -> %s [%s, %s]", job.ID, job.Cron, preview(job.Prompt, 60), frequency, storage))
	}
	return strings.Join(lines, "\n"), nil
}

func (m *CronScheduler) RunCancel(_ context.Context, input any) (string, error) {
	payload, _ := input.(map[string]any)
	id, _ := payload["job_id"].(string)
	if err := m.Cancel(id); err != nil {
		return "", err
	}
	return "Cancelled " + id, nil
}

func optionalBool(payload map[string]any, key string, fallback bool) (bool, error) {
	value, exists := payload[key]
	if !exists {
		return fallback, nil
	}
	parsed, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return parsed, nil
}
