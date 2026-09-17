package app

import (
	"context"
	"strings"
	"testing"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/mcp"
	"go-agent-harness/internal/permission"
	"go-agent-harness/internal/protocol"
)

func TestMCPHostPolicyAllowsKnownReadOnlyAndFailsClosed(t *testing.T) {
	registry := newDefaultRegistry()
	manager := mcp.New(registry)
	registerMCPTool(registry, manager)
	if _, err := manager.Connect("deploy"); err != nil {
		t.Fatal(err)
	}

	hookManager := newDefaultHooks(manager)
	ctx := permission.WithInteractive(context.Background(), false)
	if blocked := hookManager.TriggerPreToolUse(ctx, hooks.ToolCall{Name: "mcp__deploy__status"}); blocked != "" {
		t.Fatalf("Host allow policy should permit status: %s", blocked)
	}
	blocked := hookManager.TriggerPreToolUse(ctx, hooks.ToolCall{Name: "mcp__deploy__trigger"})
	if !strings.Contains(blocked, "non-interactive turns cannot request") {
		t.Fatalf("untrusted MCP tool should fail closed: %q", blocked)
	}
}

type mcpSequenceModel struct {
	t     *testing.T
	calls int
}

func (m *mcpSequenceModel) Complete(_ context.Context, request agent.ModelRequest) (agent.ModelResponse, error) {
	m.calls++
	names := toolNames(request.Tools)
	switch m.calls {
	case 1:
		if !names["connect_mcp"] || names["mcp__docs__search"] {
			m.t.Fatalf("unexpected initial tool pool: %v", names)
		}
		return toolCall("connect_1", "connect_mcp", map[string]any{"name": "docs"}), nil
	case 2:
		if !names["mcp__docs__search"] {
			m.t.Fatalf("discovered MCP tool missing on next round: %v", names)
		}
		return toolCall("search_1", "mcp__docs__search", map[string]any{"query": "agent hooks"}), nil
	default:
		m.t.Fatalf("unexpected model call: %d", m.calls)
		return agent.ModelResponse{}, nil
	}
}

func TestMCPToolAppearsAndRunsOnNextWorkerRound(t *testing.T) {
	registry := newDefaultRegistry()
	manager := mcp.New(registry)
	registerMCPTool(registry, manager)
	model := &mcpSequenceModel{t: t}
	worker := agent.NewWorker(model, registry, newDefaultHooks(manager))

	first, err := worker.RunTurn(context.Background(), "system", nil, nil)
	if err != nil || len(first.Tools.Results) != 1 || first.Tools.Results[0].IsError {
		t.Fatalf("connect round failed: %#v, %v", first, err)
	}
	second, err := worker.RunTurn(context.Background(), "system", nil, nil)
	if err != nil || len(second.Tools.Results) != 1 || second.Tools.Results[0].IsError {
		t.Fatalf("MCP call round failed: %#v, %v", second, err)
	}
	if !strings.Contains(second.Tools.Results[0].Text, "agent hooks") {
		t.Fatalf("unexpected MCP result: %#v", second.Tools.Results[0])
	}
}

func toolCall(id, name string, input map[string]any) agent.ModelResponse {
	return agent.ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{
		Type: protocol.BlockToolUse, ToolUseID: id, ToolName: name, Input: input,
	}}}}
}
