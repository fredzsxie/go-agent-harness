package agentctx

import (
	"context"
	"strings"
	"testing"

	"go-agent-harness/internal/prompt"
)

func TestRefreshPromptKeepsRequestContextAndAddsDynamicTools(t *testing.T) {
	manager := New(prompt.NewBuilder("skills", "/repo"), "")
	initial, err := manager.StartRequest(context.Background(), nil, []string{"connect_mcp"}, prompt.LiveContext{CurrentTime: "2026-09-17T10:00:00+08:00"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(initial, "mcp__docs__search") {
		t.Fatal("discovered tool should not exist before connection")
	}

	refreshed := manager.RefreshPrompt([]string{"connect_mcp", "mcp__docs__search"}, prompt.LiveContext{
		CurrentTime: "2026-09-17T10:00:01+08:00", ConnectedMCP: []string{"docs"}, ActiveTeammates: []string{"alice"},
	})
	for _, want := range []string{"mcp__docs__search", "Working directory: /repo", "Skills available:\nskills", "Connected MCP servers: docs", "Active teammates: alice", "Current time: 2026-09-17T10:00:01+08:00"} {
		if !strings.Contains(refreshed, want) {
			t.Fatalf("refreshed Prompt missing %q:\n%s", want, refreshed)
		}
	}
}
