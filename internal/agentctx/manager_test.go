package agentctx

import (
	"context"
	"strings"
	"testing"

	"go-agent-harness/internal/prompt"
)

func TestRefreshPromptKeepsRequestContextAndAddsDynamicTools(t *testing.T) {
	manager := New(prompt.NewBuilder("skills", "/repo"), "")
	initial, err := manager.StartRequest(context.Background(), nil, []string{"connect_mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(initial, "mcp__docs__search") {
		t.Fatal("discovered tool should not exist before connection")
	}

	refreshed := manager.RefreshPrompt([]string{"connect_mcp", "mcp__docs__search"})
	for _, want := range []string{"mcp__docs__search", "Working directory: /repo", "Skills available:\nskills"} {
		if !strings.Contains(refreshed, want) {
			t.Fatalf("refreshed Prompt missing %q:\n%s", want, refreshed)
		}
	}
}
