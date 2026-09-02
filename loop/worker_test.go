package loop

import (
	"context"
	"testing"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/hooks"
)

type fakeModel struct {
	response Message
	requests []agent.ModelRequest
}

func (m *fakeModel) Complete(_ context.Context, request agent.ModelRequest) (agent.ModelResponse, error) {
	m.requests = append(m.requests, request)
	return agent.ModelResponse{Message: m.response}, nil
}

func TestWorkerRunsModelAndToolsThroughOnePath(t *testing.T) {
	model := &fakeModel{response: Message{Role: RoleAssistant, Blocks: []ContentBlock{{
		Type: BlockToolUse, ToolUseID: "toolu_01", ToolName: "echo", Input: map[string]any{"text": "hello"},
	}}}}
	registry := NewRegistry()
	registry.Register(ToolSpec{Name: "echo"}, func(_ context.Context, input any) (string, error) {
		return input.(map[string]any)["text"].(string), nil
	})

	turn, err := NewWorker(model, registry, nil).RunTurn(context.Background(), "system", []Message{{Role: RoleUser, Content: "go"}}, nil)
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

func TestToolInterceptorCanReplaceHistoryAndStopBatch(t *testing.T) {
	registry := NewRegistry()
	executor := NewToolExecutor(registry, hooks.NewManager())
	blocks := []ContentBlock{
		{Type: BlockToolUse, ToolUseID: "compact_01", ToolName: "compact"},
		{Type: BlockToolUse, ToolUseID: "later_01", ToolName: "later"},
	}
	replacement := []Message{{Role: RoleUser, Content: "summary"}}

	batch, err := executor.Execute(context.Background(), nil, blocks, func(_ context.Context, _ []Message, call hooks.ToolCall) (ToolOutcome, bool, error) {
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
