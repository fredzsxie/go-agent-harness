package workflow

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go-agent-harness/internal/logger"
)

const (
	DefaultAgentCap    = 1000
	DefaultConcurrency = 8
)

type ExecutionConfig struct {
	Registry    *Registry
	Journal     *Journal
	Runner      AgentRunner
	Task        *Task
	TokenBudget int64
	AgentCap    int
	Concurrency int
}

type executionLimits struct {
	mu        sync.Mutex
	agents    int
	agentCap  int
	semaphore chan struct{}
	budget    int64
	spent     int64
}

type taskTracker struct {
	mu         sync.Mutex
	task       *Task
	phasesSeen map[string]bool
}

// Execution 实现可信脚本的编排原语；嵌套 Workflow 共享限额、journal 和任务状态。
type Execution struct {
	registry *Registry
	journal  *Journal
	runner   AgentRunner
	tracker  *taskTracker
	limits   *executionLimits
	depth    int
	phaseMu  sync.RWMutex
	phase    string
}

var _ ExecutionContext = (*Execution)(nil)

func NewExecution(config ExecutionConfig) (*Execution, error) {
	if config.Registry == nil {
		return nil, fmt.Errorf("workflow registry is required")
	}
	if config.Journal == nil {
		return nil, fmt.Errorf("workflow journal is required")
	}
	if config.Runner == nil {
		return nil, fmt.Errorf("workflow agent runner is required")
	}
	if config.Task == nil {
		return nil, fmt.Errorf("workflow task is required")
	}
	if config.TokenBudget < 0 {
		return nil, fmt.Errorf("workflow token budget cannot be negative")
	}
	if config.AgentCap <= 0 {
		config.AgentCap = DefaultAgentCap
	}
	if config.Concurrency <= 0 {
		config.Concurrency = DefaultConcurrency
	}
	return &Execution{
		registry: config.Registry,
		journal:  config.Journal,
		runner:   config.Runner,
		tracker:  &taskTracker{task: config.Task, phasesSeen: make(map[string]bool)},
		limits: &executionLimits{
			agentCap:  config.AgentCap,
			semaphore: make(chan struct{}, config.Concurrency),
			budget:    config.TokenBudget,
		},
	}, nil
}

func (e *Execution) Agent(ctx context.Context, prompt string, options AgentOptions) (any, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, fmt.Errorf("workflow agent prompt is required")
	}
	if options.Schema != nil {
		if err := ValidateSchema(options.Schema); err != nil {
			return nil, err
		}
	}
	label := strings.TrimSpace(options.Label)
	if label == "" {
		label = previewLabel(prompt)
	}
	if err := e.limits.claimAgent(); err != nil {
		return nil, err
	}
	key, err := SemanticKey("agent", label, prompt, options.Schema)
	if err != nil {
		return nil, err
	}
	phase := e.agentPhase(options.Phase)
	if cached, found, err := e.journal.Cached(key); err != nil {
		return nil, err
	} else if found {
		if options.Schema != nil {
			if err := ValidateValue(cached, options.Schema); err != nil {
				return nil, fmt.Errorf("cached workflow agent output is invalid: %w", err)
			}
		}
		e.progress("workflow_agent", map[string]any{"label": label, "phase": phase, "status": "cached"})
		logger.Debug("[Workflow] agent cached label=%s", label)
		return cached, nil
	}

	e.progress("workflow_agent", map[string]any{"label": label, "phase": phase, "status": "started"})
	logger.Info("[Workflow] agent started label=%s", label)
	result, err := e.runAgent(ctx, prompt, options.Schema, label)
	if err != nil {
		logger.Error("[Workflow] agent failed label=%s: %v", label, err)
		return nil, err
	}
	tokens := result.Tokens
	if options.Schema != nil {
		if validationErr := ValidateValue(result.Value, options.Schema); validationErr != nil {
			logger.Warn("[Workflow] agent returned invalid structured output, retrying label=%s: %v", label, validationErr)
			retry, retryErr := e.runAgent(ctx, prompt+"\n\nReturn valid JSON.", options.Schema, label)
			if retryErr != nil {
				logger.Error("[Workflow] agent retry failed label=%s: %v", label, retryErr)
				return nil, retryErr
			}
			tokens += retry.Tokens
			result = retry
			if err := ValidateValue(result.Value, options.Schema); err != nil {
				logger.Error("[Workflow] agent returned invalid structured output after retry label=%s: %v", label, err)
				return nil, fmt.Errorf("workflow agent returned invalid structured output after retry: %w", err)
			}
		}
	}
	if err := e.limits.addTokens(tokens); err != nil {
		logger.Error("[Workflow] agent usage rejected label=%s: %v", label, err)
		return nil, err
	}
	if err := e.journal.Record(key, result.Value); err != nil {
		logger.Error("[Workflow] checkpoint failed label=%s: %v", label, err)
		return nil, err
	}
	e.tracker.addUsage(tokens)
	e.progress("workflow_agent", map[string]any{"label": label, "phase": phase, "status": "done"})
	logger.Info("[Workflow] agent completed label=%s tokens=%d", label, tokens)
	return result.Value, nil
}

func (e *Execution) runAgent(ctx context.Context, prompt string, schema map[string]any, label string) (AgentResult, error) {
	select {
	case e.limits.semaphore <- struct{}{}:
		defer func() { <-e.limits.semaphore }()
	case <-ctx.Done():
		return AgentResult{}, ctx.Err()
	}
	return e.runner.Run(ctx, prompt, schema, label)
}

// Parallel 是 barrier：全部 Step 结束后才返回，并保留输入顺序。
func (e *Execution) Parallel(ctx context.Context, steps []Step) ([]any, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]any, len(steps))
	errorsByIndex := make([]error, len(steps))
	var wait sync.WaitGroup
	for index, step := range steps {
		index, step := index, step
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[index], errorsByIndex[index] = callStep(ctx, step)
			if errorsByIndex[index] != nil {
				cancel()
			}
		}()
	}
	wait.Wait()
	for _, err := range errorsByIndex {
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

// Pipeline 让每个 item 独立完成全部 Stage，不在相邻 Stage 之间设置全局 barrier。
func (e *Execution) Pipeline(ctx context.Context, items []any, stages ...Stage) ([]any, error) {
	steps := make([]Step, len(items))
	for index, item := range items {
		index, item := index, item
		steps[index] = func(ctx context.Context) (any, error) {
			value := item
			for _, stage := range stages {
				if stage == nil {
					return nil, fmt.Errorf("workflow pipeline stage is nil")
				}
				var err error
				value, err = stage(ctx, value, item, index)
				if err != nil {
					return nil, err
				}
			}
			return value, nil
		}
	}
	return e.Parallel(ctx, steps)
}

func (e *Execution) Phase(title string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	e.phaseMu.Lock()
	e.phase = title
	e.phaseMu.Unlock()
	if e.tracker.addPhase(title) {
		logger.Info("[Workflow] phase=%s", title)
	}
}

func (e *Execution) Log(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	e.progress("workflow_log", map[string]any{"message": message})
	logger.Info("[Workflow] %s", message)
}

func (e *Execution) Workflow(ctx context.Context, name string, args map[string]any) (any, error) {
	if e.depth >= 1 {
		return nil, fmt.Errorf("workflow nesting is limited to one level")
	}
	definition, ok := e.registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("unknown workflow %q", name)
	}
	if args == nil {
		args = map[string]any{}
	}
	child := &Execution{
		registry: e.registry, journal: e.journal, runner: e.runner,
		tracker: e.tracker, limits: e.limits, depth: e.depth + 1,
	}
	return definition.Script(ctx, child, args)
}

func (e *Execution) SnapshotTask() Task {
	return e.tracker.snapshot()
}

func (e *Execution) progress(eventType string, details map[string]any) {
	e.tracker.progress(eventType, details)
}

func (e *Execution) agentPhase(explicit string) string {
	if explicit = strings.TrimSpace(explicit); explicit != "" {
		return explicit
	}
	e.phaseMu.RLock()
	defer e.phaseMu.RUnlock()
	return e.phase
}

func (l *executionLimits) claimAgent() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.agents++
	if l.agents > l.agentCap {
		return fmt.Errorf("workflow agent call cap reached (%d)", l.agentCap)
	}
	return nil
}

func (l *executionLimits) addTokens(tokens int64) error {
	if tokens < 0 {
		return fmt.Errorf("workflow agent returned negative token usage")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.budget > 0 && l.spent+tokens > l.budget {
		return fmt.Errorf("workflow token budget exceeded (%d > %d)", l.spent+tokens, l.budget)
	}
	l.spent += tokens
	return nil
}

func (t *taskTracker) progress(eventType string, details map[string]any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.task.Progress = append(t.task.Progress, Progress{Type: eventType, Details: cloneDetails(details)})
}

func (t *taskTracker) addPhase(title string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.phasesSeen[title] {
		return false
	}
	t.phasesSeen[title] = true
	t.task.Progress = append(t.task.Progress, Progress{Type: "workflow_phase", Details: map[string]any{"title": title}})
	return true
}

func (t *taskTracker) addUsage(tokens int64) {
	t.mu.Lock()
	t.task.Usage.Agents++
	t.task.Usage.Tokens += tokens
	t.mu.Unlock()
}

func (t *taskTracker) snapshot() Task {
	t.mu.Lock()
	defer t.mu.Unlock()
	copyTask := *t.task
	copyTask.Progress = make([]Progress, len(t.task.Progress))
	for index, progress := range t.task.Progress {
		copyTask.Progress[index] = Progress{Type: progress.Type, Details: cloneDetails(progress.Details)}
	}
	return copyTask
}

func callStep(ctx context.Context, step Step) (value any, err error) {
	if step == nil {
		return nil, fmt.Errorf("workflow parallel step is nil")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("workflow step panicked: %v", recovered)
		}
	}()
	return step(ctx)
}

func previewLabel(prompt string) string {
	runes := []rune(prompt)
	if len(runes) > 24 {
		return string(runes[:24]) + "..."
	}
	return prompt
}

func cloneDetails(details map[string]any) map[string]any {
	if details == nil {
		return nil
	}
	copyDetails := make(map[string]any, len(details))
	for key, value := range details {
		copyDetails[key] = value
	}
	return copyDetails
}
