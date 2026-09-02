package subagent

import (
	"testing"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/protocol"
)

func TestLatestAssistantTextFallsBackToMostRecentAssistantMessage(t *testing.T) {
	messages := []protocol.Message{
		{Role: protocol.RoleUser, Content: "delegate this"},
		{Role: protocol.RoleAssistant, Content: "first answer"},
		{Role: protocol.RoleUser, Blocks: []protocol.ContentBlock{
			{Type: protocol.BlockToolResult, ToolUseID: "x", Text: "tool output"},
		}},
		{Role: protocol.RoleAssistant, Content: "final summary"},
	}

	result := agent.LatestAssistantText(messages)
	if result != "final summary" {
		t.Fatalf("expected final assistant summary, got %q", result)
	}
}
