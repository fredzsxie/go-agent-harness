package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type countingRunner struct {
	mu     sync.Mutex
	calls  int
	result AgentResult
}

func (r *countingRunner) Run(context.Context, string, map[string]any, string) (AgentResult, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return r.result, nil
}

func (r *countingRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestManagerCompletesAndResumesOneToolRun(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := registry.Register(Definition{
		Metadata: Metadata{Name: "simple", Description: "simple workflow", Phases: []string{"Work"}},
		Script: func(ctx context.Context, execution ExecutionContext, args map[string]any) (any, error) {
			execution.Phase("Work")
			value, err := execution.Agent(ctx, "process "+args["value"].(string), AgentOptions{Label: "work"})
			if err != nil {
				return nil, err
			}
			execution.Log("work completed")
			return map[string]any{"value": value}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	runner := &countingRunner{result: AgentResult{Value: "done", Tokens: 3}}
	store := NewStore(StoreConfig{WorkDir: root})
	manager := NewManager(ManagerConfig{Registry: registry, Store: store, Runner: runner})

	first, err := manager.Run(context.Background(), ToolInput{Name: "simple", Args: map[string]any{"value": "one"}, HasArgs: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.Launched.Status != "async_launched" || first.Task.Status != StatusCompleted || first.Task.Usage != (Usage{Agents: 1, Tokens: 3}) {
		t.Fatalf("unexpected first run: %#v", first)
	}
	if _, err := os.Stat(filepath.Join(root, first.Task.OutputFile)); err != nil {
		t.Fatalf("output artifact missing: %v", err)
	}

	resumed, err := manager.Run(context.Background(), ToolInput{Name: "simple", ResumeFromRunID: first.Task.RunID})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Task.Status != StatusCompleted || resumed.Task.Usage != (Usage{}) || runner.count() != 1 {
		t.Fatalf("resume did not use journal cache: task=%#v calls=%d", resumed.Task, runner.count())
	}
	if _, err := manager.Run(context.Background(), ToolInput{
		Name: "simple", Args: map[string]any{"value": "changed"}, HasArgs: true, ResumeFromRunID: first.Task.RunID,
	}); err == nil {
		t.Fatal("expected changed resume args to fail")
	}
}

func TestManagerPersistsFailedWorkflowAsTaskResult(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Definition{
		Metadata: Metadata{Name: "fails", Description: "fails"},
		Script: func(context.Context, ExecutionContext, map[string]any) (any, error) {
			return nil, fmt.Errorf("script failed")
		},
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(ManagerConfig{
		Registry: registry, Store: NewStore(StoreConfig{WorkDir: t.TempDir()}),
		Runner: &countingRunner{},
	})
	result, err := manager.Run(context.Background(), ToolInput{Name: "fails", Args: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Task.Status != StatusFailed || result.Result.(map[string]any)["error"] != "script failed" {
		t.Fatalf("unexpected failed task result: %#v", result)
	}
}

func TestManagerRunToolReturnsJSONAndRejectsUnknownFields(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Definition{
		Metadata: Metadata{Name: "empty", Description: "empty"},
		Script: func(context.Context, ExecutionContext, map[string]any) (any, error) {
			return map[string]any{"ok": true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(ManagerConfig{
		Registry: registry, Store: NewStore(StoreConfig{WorkDir: t.TempDir()}), Runner: &countingRunner{},
	})
	output, err := manager.RunTool(context.Background(), map[string]any{"name": "empty"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded Result
	if err := json.Unmarshal([]byte(output), &decoded); err != nil || decoded.Task.Status != StatusCompleted {
		t.Fatalf("unexpected tool output: %s, %v", output, err)
	}
	if _, err := manager.RunTool(context.Background(), map[string]any{"name": "empty", "script": "inject"}); err == nil {
		t.Fatal("expected unknown tool field to fail")
	}
}
