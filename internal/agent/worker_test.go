package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/protocol"
	"go-agent-harness/internal/tool"
)

type fakeModel struct {
	response protocol.Message
	requests []llm.Request
}

func (m *fakeModel) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	m.requests = append(m.requests, request)
	return llm.Response{Message: m.response}, nil
}

func TestWorkerRunsModelAndToolsThroughOnePath(t *testing.T) {
	model := &fakeModel{response: protocol.Message{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{
		Type: protocol.BlockToolUse, ToolUseID: "toolu_01", ToolName: "echo", Input: map[string]any{"text": "hello"},
	}}}}
	registry := tool.NewRegistry()
	registry.Register(tool.Spec{Name: "echo"}, func(_ context.Context, input any) (string, error) {
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
	model := &responseModel{response: llm.Response{
		Message: protocol.Message{Role: protocol.RoleAssistant, Content: "done"},
		Usage:   llm.Usage{InputTokens: 12, OutputTokens: 5},
	}}
	turn, err := NewWorker(model, tool.NewRegistry(), nil).RunTurn(context.Background(), "system", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Usage != (llm.Usage{InputTokens: 12, OutputTokens: 5}) {
		t.Fatalf("unexpected usage: %#v", turn.Usage)
	}
}

func TestToolInterceptorCanReplaceHistoryAndStopBatch(t *testing.T) {
	registry := tool.NewRegistry()
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
	registry := tool.NewRegistry()
	executed := false
	registry.Register(tool.Spec{Name: "echo"}, func(_ context.Context, _ any) (string, error) {
		executed = true
		return "unexpected", nil
	})

	modelWithStop := &responseModel{response: llm.Response{Message: model.response, StopReason: "max_tokens"}}
	turn, err := NewWorker(modelWithStop, registry, nil).RunTurnWithOptions(context.Background(), "system", nil, nil, TurnOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !turn.HasTools || turn.StopReason != "max_tokens" || executed {
		t.Fatalf("truncated tool call should be deferred: turn=%#v executed=%v", turn, executed)
	}
}

type responseModel struct {
	response llm.Response
}

func (m *responseModel) Complete(context.Context, llm.Request) (llm.Response, error) {
	return m.response, nil
}

func TestWorkerRetriesTruncationWithoutReplayingPartialTool(t *testing.T) {
	partial := protocol.Message{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{Type: protocol.BlockToolUse, ToolUseID: "partial", ToolName: "echo", Input: map[string]any{"text": "partial"}}}}
	complete := protocol.CloneMessages([]protocol.Message{partial})[0]
	complete.Blocks[0].ToolUseID = "complete"
	complete.Blocks[0].Input["text"] = "complete"
	model := &scriptedModel{results: []scriptedModelResult{
		{response: llm.Response{Message: partial, StopReason: "max_tokens", Usage: llm.Usage{OutputTokens: 1}}},
		{response: llm.Response{Message: complete, StopReason: "tool_use", Usage: llm.Usage{OutputTokens: 2}}},
	}}
	registry := tool.NewRegistry()
	calls := 0
	registry.Register(tool.Spec{Name: "echo"}, func(_ context.Context, input any) (string, error) {
		calls++
		return input.(map[string]any)["text"].(string), nil
	})
	messages := []protocol.Message{{Role: protocol.RoleUser, Content: "do work"}}
	turn, err := NewWorker(model, registry, nil).RunTurn(context.Background(), "system", messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || turn.Tools.Results[0].Text != "complete" || turn.Usage.OutputTokens != 3 {
		t.Fatalf("unexpected turn: %#v, calls=%d", turn, calls)
	}
	if len(model.requests) != 2 || model.requests[1].MaxTokens != escalatedMaxTokens || !reflect.DeepEqual(model.requests[0].Messages, model.requests[1].Messages) {
		t.Fatalf("retry should use original history: %#v", model.requests)
	}
}

func TestWorkerStopsAfterSecondTruncation(t *testing.T) {
	model := &responseModel{response: llm.Response{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "partial"}, StopReason: "max_tokens"}}
	_, err := NewWorker(model, tool.NewRegistry(), nil).RunTurn(context.Background(), "system", nil, nil)
	if !errors.Is(err, ErrResponseTruncated) {
		t.Fatalf("expected bounded truncation failure, got %v", err)
	}
}
