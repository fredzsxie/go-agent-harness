package todo

import (
	"context"
	"strings"
	"testing"
)

func TestRunWriteAcceptsEncodedArrays(t *testing.T) {
	for _, encoded := range []string{
		`[{"content":"inspect repo","status":"pending"}]`,
		`[{'content': 'write tests', 'status': 'in_progress'}]`,
	} {
		manager := NewManager(nil)
		output, err := manager.RunWrite(context.Background(), map[string]any{"todos": encoded})
		if err != nil {
			t.Fatalf("encoded todos rejected: %v", err)
		}
		if len(manager.Snapshot()) != 1 || !strings.Contains(output, "(0/1 completed)") {
			t.Fatalf("unexpected todo state/output: %#v %q", manager.Snapshot(), output)
		}
	}
}

func TestRunWriteRejectsInvalidUpdateWithoutReplacingState(t *testing.T) {
	manager := NewManager(nil)
	_, err := manager.RunWrite(context.Background(), map[string]any{"todos": []any{
		map[string]any{"content": "keep", "status": "pending"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = manager.RunWrite(context.Background(), map[string]any{"todos": []any{
		map[string]any{"content": "first", "status": "in_progress"},
		map[string]any{"content": "second", "status": "in_progress"},
	}})
	if err == nil {
		t.Fatal("expected multiple in_progress todos to be rejected")
	}
	if snapshot := manager.Snapshot(); len(snapshot) != 1 || snapshot[0].Content != "keep" {
		t.Fatalf("invalid update replaced state: %#v", snapshot)
	}
}

func TestRunWriteDoesNotEvaluateStringInput(t *testing.T) {
	manager := NewManager(nil)
	_, err := manager.RunWrite(context.Background(), map[string]any{
		"todos": `__import__('os').system('touch should-not-exist')`,
	})
	if err == nil {
		t.Fatal("expected expression-like input to be rejected")
	}
}
