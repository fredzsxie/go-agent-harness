package subagent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/protocol"
	"go-agent-harness/internal/tool"
)

type queueModel struct {
	responses []llm.Response
	requests  []llm.Request
}

func (m *queueModel) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	request.Messages = protocol.CloneMessages(request.Messages)
	m.requests = append(m.requests, request)
	if len(m.responses) == 0 {
		return llm.Response{}, errors.New("unexpected model call")
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

func TestRunTaskIsolatesHistoryAndRecoversTruncatedToolCall(t *testing.T) {
	toolMessage := protocol.Message{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{Type: protocol.BlockToolUse, ToolUseID: "read", ToolName: "read_file", Input: map[string]any{"path": "README.md"}}}}
	model := &queueModel{responses: []llm.Response{
		{Message: toolMessage, StopReason: "max_tokens"},
		{Message: toolMessage, StopReason: "tool_use"},
		{Message: protocol.Message{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{Type: protocol.BlockText, Text: "first summary"}}}},
		{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "second summary"}},
	}}
	calls, pre, post := 0, 0, 0
	registry := tool.NewRegistry()
	registry.Register(tool.Spec{Name: "read_file"}, func(context.Context, any) (string, error) { calls++; return "private tool output", nil })
	hookManager := hooks.NewManager()
	hookManager.BeforeTool(func(context.Context, hooks.ToolCall) string { pre++; return "" })
	hookManager.AfterTool(func(hooks.ToolCall, string) { post++ })
	manager := New(model, registry, hookManager)
	first, err := manager.RunTask(context.Background(), map[string]any{"description": "first task"})
	if err != nil || first != "first summary" {
		t.Fatalf("first task = %q, %v", first, err)
	}
	second, err := manager.RunTask(context.Background(), map[string]any{"description": "second task"})
	if err != nil || second != "second summary" {
		t.Fatalf("second task = %q, %v", second, err)
	}
	if calls != 1 || pre != 1 || post != 1 {
		t.Fatalf("partial tools or hooks executed: %d/%d/%d", calls, pre, post)
	}
	if !reflect.DeepEqual(model.requests[0].Messages, model.requests[1].Messages) || model.requests[1].MaxTokens <= model.requests[0].MaxTokens {
		t.Fatal("truncation retry did not preserve input")
	}
	if len(model.requests[2].Messages) != 3 || model.requests[2].Messages[2].Blocks[0].ToolUseID != "read" {
		t.Fatal("missing matching tool result")
	}
	if got := model.requests[3].Messages; len(got) != 1 || got[0].Content != "second task" {
		t.Fatalf("child histories leaked: %#v", got)
	}
	for _, request := range model.requests {
		if len(request.Tools) != 1 || request.Tools[0].Name != "read_file" {
			t.Fatalf("child gained unregistered tools: %#v", request.Tools)
		}
	}
}

func TestRunTaskValidationAndBoundedRecovery(t *testing.T) {
	model := &queueModel{responses: []llm.Response{
		{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "partial"}, StopReason: "max_tokens"},
		{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "partial"}, StopReason: "max_tokens"},
	}}
	manager := New(model, tool.NewRegistry(), nil)
	for _, input := range []any{nil, "task", map[string]any{}, map[string]any{"description": " "}} {
		if _, err := manager.RunTask(context.Background(), input); err == nil {
			t.Fatalf("accepted invalid input: %#v", input)
		}
	}
	if len(model.requests) != 0 {
		t.Fatal("invalid task reached model")
	}
	if _, err := manager.RunTask(context.Background(), map[string]any{"description": "task"}); !errors.Is(err, agent.ErrResponseTruncated) {
		t.Fatalf("expected truncation error, got %v", err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("unbounded retry: %d", len(model.requests))
	}
}
