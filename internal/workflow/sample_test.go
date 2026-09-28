package workflow

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type reviewRunner struct{}

func (reviewRunner) Run(_ context.Context, _ string, _ map[string]any, label string) (AgentResult, error) {
	if strings.HasPrefix(label, "audit:") {
		return AgentResult{Value: map[string]any{"findings": []any{map[string]any{
			"title": label, "severity": "medium",
		}}}, Tokens: 1}, nil
	}
	if strings.HasPrefix(label, "verify:") {
		return AgentResult{Value: map[string]any{"isReal": true, "reason": "confirmed"}, Tokens: 1}, nil
	}
	return AgentResult{}, fmt.Errorf("unexpected label %s", label)
}

func TestReviewChangesWorkflowRunsAuditAndVerifyPipeline(t *testing.T) {
	registry := NewRegistry()
	if err := RegisterDefaults(registry); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(ManagerConfig{
		Registry: registry, Store: NewStore(StoreConfig{WorkDir: t.TempDir()}), Runner: reviewRunner{},
	})
	result, err := manager.Run(context.Background(), ToolInput{
		Name: "review-changes", Args: map[string]any{"changes": "diff --git a/a.go b/a.go"}, HasArgs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	confirmed := result.Result.(map[string]any)["confirmed"].([]map[string]any)
	if len(confirmed) != 4 || result.Task.Usage != (Usage{Agents: 8, Tokens: 8}) {
		t.Fatalf("unexpected review result: %#v, usage=%#v", confirmed, result.Task.Usage)
	}
}
