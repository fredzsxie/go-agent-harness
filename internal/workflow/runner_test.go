package workflow

import (
	"context"
	"testing"

	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/protocol"
)

type captureWorkflowModel struct {
	request llm.Request
}

func (m *captureWorkflowModel) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	m.request = request
	return llm.Response{
		Message: protocol.Message{Role: protocol.RoleAssistant, Content: "```json\n{\"ok\":true}\n```"},
		Usage:   llm.Usage{InputTokens: 11, OutputTokens: 4},
	}, nil
}

func TestModelRunnerUsesRestrictedModelCall(t *testing.T) {
	model := &captureWorkflowModel{}
	runner := NewModelRunner(model, 1200)
	result, err := runner.Run(context.Background(), "check", map[string]any{
		"type": "object", "required": []string{"ok"}, "properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
	}, "audit")
	if err != nil {
		t.Fatal(err)
	}
	if len(model.request.Tools) != 0 || model.request.MaxTokens != 1200 || model.request.System != workflowAgentSystemPrompt {
		t.Fatalf("unexpected workflow model request: %#v", model.request)
	}
	if len(model.request.Messages) != 1 || model.request.Messages[0].Role != protocol.RoleUser {
		t.Fatalf("unexpected workflow messages: %#v", model.request.Messages)
	}
	if result.Value.(map[string]any)["ok"] != true || result.Tokens != 15 {
		t.Fatalf("unexpected workflow result: %#v", result)
	}
}

func TestParseJSONOutputRejectsTrailingContent(t *testing.T) {
	if _, err := parseJSONOutput(`{"ok":true} trailing`); err == nil {
		t.Fatal("expected trailing content to fail")
	}
}
