package subagent

import (
	"testing"

	"go-agent-harness/internal/agent"
)

func TestLatestAssistantTextFallsBackToMostRecentAssistantMessage(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.RoleUser, Content: "delegate this"},
		{Role: agent.RoleAssistant, Content: "first answer"},
		{Role: agent.RoleUser, Blocks: []agent.ContentBlock{
			{Type: agent.BlockToolResult, ToolUseID: "x", Text: "tool output"},
		}},
		{Role: agent.RoleAssistant, Content: "final summary"},
	}

	result := agent.LatestAssistantText(messages)
	if result != "final summary" {
		t.Fatalf("expected final assistant summary, got %q", result)
	}
}
