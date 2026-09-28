package workflow

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type runnerReply struct {
	result AgentResult
	err    error
}

type sequenceAgentRunner struct {
	mu      sync.Mutex
	replies []runnerReply
	calls   int
}

func (r *sequenceAgentRunner) Run(context.Context, string, map[string]any, string) (AgentResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	index := r.calls
	r.calls++
	if index >= len(r.replies) {
		return AgentResult{}, fmt.Errorf("unexpected runner call")
	}
	return r.replies[index].result, r.replies[index].err
}

func (r *sequenceAgentRunner) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func newTestExecution(t *testing.T, runner AgentRunner, configure func(*ExecutionConfig)) *Execution {
	t.Helper()
	store := NewStore(StoreConfig{WorkDir: t.TempDir()})
	runID, err := store.ReserveRun("test")
	if err != nil {
		t.Fatal(err)
	}
	journal, err := store.OpenJournal(runID, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = journal.Close() })
	config := ExecutionConfig{
		Registry: NewRegistry(), Journal: journal, Runner: runner,
		Task: &Task{TaskID: "local_" + runID, TaskType: TaskType, RunID: runID, Workflow: "test", Status: StatusRunning},
	}
	if configure != nil {
		configure(&config)
	}
	execution, err := NewExecution(config)
	if err != nil {
		t.Fatal(err)
	}
	return execution
}

func TestExecutionRetriesStructuredOutputAndCachesResult(t *testing.T) {
	runner := &sequenceAgentRunner{replies: []runnerReply{
		{result: AgentResult{Value: "invalid", Tokens: 2}},
		{result: AgentResult{Value: map[string]any{"ok": true}, Tokens: 3}},
	}}
	execution := newTestExecution(t, runner, nil)
	schema := map[string]any{
		"type": "object", "required": []string{"ok"}, "properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
	}
	options := AgentOptions{Schema: schema, Label: "audit", Phase: "Review"}
	value, err := execution.Agent(context.Background(), "review", options)
	if err != nil || value.(map[string]any)["ok"] != true {
		t.Fatalf("unexpected structured result: %#v, %v", value, err)
	}
	if _, err := execution.Agent(context.Background(), "review", options); err != nil {
		t.Fatal(err)
	}
	if runner.callCount() != 2 {
		t.Fatalf("expected retry followed by cache hit, got %d calls", runner.callCount())
	}
	task := execution.SnapshotTask()
	if task.Usage.Agents != 1 || task.Usage.Tokens != 5 {
		t.Fatalf("unexpected workflow usage: %#v", task.Usage)
	}
}

func TestExecutionRejectsInvalidCachedStructuredOutput(t *testing.T) {
	runner := &sequenceAgentRunner{}
	execution := newTestExecution(t, runner, nil)
	schema := map[string]any{
		"type": "object", "required": []string{"ok"}, "properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
	}
	key, err := SemanticKey("agent", "cached", "review", schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := execution.journal.Record(key, map[string]any{"ok": "wrong"}); err != nil {
		t.Fatal(err)
	}
	if _, err := execution.Agent(context.Background(), "review", AgentOptions{Schema: schema, Label: "cached"}); err == nil {
		t.Fatal("expected invalid cached output to fail")
	}
	if runner.callCount() != 0 {
		t.Fatal("invalid cached output must not silently rerun")
	}
}

func TestExecutionFailsAfterOneStructuredOutputRetry(t *testing.T) {
	runner := &sequenceAgentRunner{replies: []runnerReply{
		{result: AgentResult{Value: "invalid", Tokens: 1}},
		{result: AgentResult{Value: "still invalid", Tokens: 1}},
	}}
	execution := newTestExecution(t, runner, nil)
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	if _, err := execution.Agent(context.Background(), "review", AgentOptions{Schema: schema, Label: "retry"}); err == nil {
		t.Fatal("expected invalid retry output to fail")
	}
	if runner.callCount() != 2 {
		t.Fatalf("expected exactly one retry, got %d calls", runner.callCount())
	}
}

type concurrencyRunner struct {
	mu        sync.Mutex
	active    int
	maxActive int
}

func (r *concurrencyRunner) Run(ctx context.Context, prompt string, _ map[string]any, _ string) (AgentResult, error) {
	r.mu.Lock()
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.mu.Unlock()
	select {
	case <-time.After(15 * time.Millisecond):
	case <-ctx.Done():
		return AgentResult{}, ctx.Err()
	}
	r.mu.Lock()
	r.active--
	r.mu.Unlock()
	return AgentResult{Value: prompt, Tokens: 1}, nil
}

func TestExecutionParallelHonorsConcurrencyAndOrder(t *testing.T) {
	runner := &concurrencyRunner{}
	execution := newTestExecution(t, runner, func(config *ExecutionConfig) { config.Concurrency = 2 })
	steps := make([]Step, 5)
	for index := range steps {
		index := index
		steps[index] = func(ctx context.Context) (any, error) {
			return execution.Agent(ctx, fmt.Sprintf("item-%d", index), AgentOptions{Label: fmt.Sprintf("item-%d", index)})
		}
	}
	results, err := execution.Parallel(context.Background(), steps)
	if err != nil {
		t.Fatal(err)
	}
	for index, result := range results {
		if result != fmt.Sprintf("item-%d", index) {
			t.Fatalf("parallel result order changed: %#v", results)
		}
	}
	if runner.maxActive != 2 {
		t.Fatalf("expected concurrency 2, got %d", runner.maxActive)
	}
}

func TestExecutionPipelineHasNoCrossItemBarrier(t *testing.T) {
	execution := newTestExecution(t, &sequenceAgentRunner{}, nil)
	releaseSlow := make(chan struct{})
	fastReachedSecondStage := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := execution.Pipeline(context.Background(), []any{"slow", "fast"},
			func(_ context.Context, value, _ any, _ int) (any, error) {
				if value == "slow" {
					<-releaseSlow
				}
				return value, nil
			},
			func(_ context.Context, value, _ any, _ int) (any, error) {
				if value == "fast" {
					close(fastReachedSecondStage)
				}
				return value, nil
			},
		)
		done <- err
	}()
	select {
	case <-fastReachedSecondStage:
	case <-time.After(time.Second):
		t.Fatal("fast item did not advance before slow item completed")
	}
	close(releaseSlow)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExecutionSharesLimitsWithNestedWorkflow(t *testing.T) {
	runner := &sequenceAgentRunner{replies: []runnerReply{{result: AgentResult{Value: "ok", Tokens: 1}}}}
	execution := newTestExecution(t, runner, func(config *ExecutionConfig) {
		config.AgentCap = 1
		config.TokenBudget = 1
		if err := config.Registry.Register(Definition{
			Metadata: Metadata{Name: "child", Description: "child"},
			Script: func(ctx context.Context, child ExecutionContext, _ map[string]any) (any, error) {
				if _, err := child.Agent(ctx, "nested", AgentOptions{Label: "nested"}); err != nil {
					return nil, err
				}
				return child.Workflow(ctx, "child", nil)
			},
		}); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := execution.Workflow(context.Background(), "child", nil); err == nil || err.Error() != "workflow nesting is limited to one level" {
		t.Fatalf("expected nesting limit error, got %v", err)
	}
	if _, err := execution.Agent(context.Background(), "second", AgentOptions{Label: "second"}); err == nil {
		t.Fatal("expected nested agent to consume the shared agent cap")
	}
	task := execution.SnapshotTask()
	if task.Usage.Agents != 1 || task.Usage.Tokens != 1 {
		t.Fatalf("unexpected shared usage: %#v", task.Usage)
	}
}

func TestExecutionRejectsTokenBudgetOverflow(t *testing.T) {
	runner := &sequenceAgentRunner{replies: []runnerReply{{result: AgentResult{Value: "ok", Tokens: 2}}}}
	execution := newTestExecution(t, runner, func(config *ExecutionConfig) { config.TokenBudget = 1 })
	if _, err := execution.Agent(context.Background(), "over budget", AgentOptions{Label: "budget"}); err == nil {
		t.Fatal("expected token budget overflow")
	}
	if task := execution.SnapshotTask(); task.Usage != (Usage{}) {
		t.Fatalf("budget failure must not checkpoint usage: %#v", task.Usage)
	}
}
