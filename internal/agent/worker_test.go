package agent

import (
	"context"
	"testing"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/protocol"
)

type fakeModel struct {
	response protocol.Message
	requests []ModelRequest
}

func (m *fakeModel) Complete(_ context.Context, request ModelRequest) (ModelResponse, error) {
	m.requests = append(m.requests, request)
	return ModelResponse{Message: m.response}, nil
}

func TestWorkerRunsModelAndToolsThroughOnePath(t *testing.T) {
	model := &fakeModel{response: protocol.Message{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{
		Type: protocol.BlockToolUse, ToolUseID: "toolu_01", ToolName: "echo", Input: map[string]any{"text": "hello"},
	}}}}
	registry := NewRegistry()
	registry.Register(ToolSpec{Name: "echo"}, func(_ context.Context, input any) (string, error) {
		return input.(map[string]any)["text"].(string), nil
	})

	turn, err := NewWorker(model, registry, nil).RunTurn(context.Background(), "system", []protocol.Message{{Role: protocol.RoleUser, Content: "go"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !turn.HasTools || turn.Tools.Count != 1 || len(turn.Tools.Results) != 1 {
		t.Fatalf("unexpected turn: %#v", turn)
	}
	if turn.Tools.Results[0].Text != "hello" || turn.Tools.Results[0].ToolUseID != "toolu_01" {
		t.Fatalf("unexpected tool result: %#v", turn.Tools.Results[0])
	}
	if len(model.requests) != 1 || model.requests[0].System != "system" {
		t.Fatalf("unexpected model requests: %#v", model.requests)
	}
}

func TestWorkerPreservesModelUsage(t *testing.T) {
	model := &responseModel{response: ModelResponse{
		Message: protocol.Message{Role: protocol.RoleAssistant, Content: "done"},
		Usage:   TokenUsage{InputTokens: 12, OutputTokens: 5},
	}}
	turn, err := NewWorker(model, NewRegistry(), nil).RunTurn(context.Background(), "system", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Usage != (TokenUsage{InputTokens: 12, OutputTokens: 5}) {
		t.Fatalf("unexpected usage: %#v", turn.Usage)
	}
}

func TestToolInterceptorCanReplaceHistoryAndStopBatch(t *testing.T) {
	registry := NewRegistry()
	executor := NewToolExecutor(registry, hooks.NewManager())
	blocks := []protocol.ContentBlock{
		{Type: protocol.BlockToolUse, ToolUseID: "compact_01", ToolName: "compact"},
		{Type: protocol.BlockToolUse, ToolUseID: "later_01", ToolName: "later"},
	}
	replacement := []protocol.Message{{Role: protocol.RoleUser, Content: "summary"}}

	batch, err := executor.Execute(context.Background(), nil, blocks, func(_ context.Context, _ []protocol.Message, call hooks.ToolCall) (ToolOutcome, bool, error) {
		if call.Name != "compact" {
			t.Fatalf("unexpected call after stop: %s", call.Name)
		}
		return ToolOutcome{Text: "compacted", Stop: true, Messages: replacement}, true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !batch.Stop || batch.Count != 1 || len(batch.Results) != 1 || len(batch.Messages) != 1 {
		t.Fatalf("unexpected batch: %#v", batch)
	}
}

func TestWorkerDefersToolExecutionWhenResponseIsTruncated(t *testing.T) {
	model := &fakeModel{response: protocol.Message{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{
		Type: protocol.BlockToolUse, ToolUseID: "toolu_partial", ToolName: "echo", Input: map[string]any{"text": "partial"},
	}}}}
	registry := NewRegistry()
	executed := false
	registry.Register(ToolSpec{Name: "echo"}, func(_ context.Context, _ any) (string, error) {
		executed = true
		return "unexpected", nil
	})

	modelWithStop := &responseModel{response: ModelResponse{Message: model.response, StopReason: "max_tokens"}}
	turn, err := NewWorker(modelWithStop, registry, nil).RunTurn(context.Background(), "system", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !turn.HasTools || turn.StopReason != "max_tokens" || executed {
		t.Fatalf("truncated tool call should be deferred: turn=%#v executed=%v", turn, executed)
	}
}

type responseModel struct {
	response ModelResponse
}

func (m *responseModel) Complete(context.Context, ModelRequest) (ModelResponse, error) {
	return m.response, nil
}
