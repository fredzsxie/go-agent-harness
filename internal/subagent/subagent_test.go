package subagent

import (
	"testing"

	"go-agent-harness/loop"
)

func TestLatestAssistantTextFallsBackToMostRecentAssistantMessage(t *testing.T) {
	messages := []loop.Message{
		{Role: loop.RoleUser, Content: "delegate this"},
		{Role: loop.RoleAssistant, Content: "first answer"},
		{Role: loop.RoleUser, Blocks: []loop.ContentBlock{
			{Type: loop.BlockToolResult, ToolUseID: "x", Text: "tool output"},
		}},
		{Role: loop.RoleAssistant, Content: "final summary"},
	}

	result := loop.LatestAssistantText(messages)
	if result != "final summary" {
		t.Fatalf("expected final assistant summary, got %q", result)
	}
}

func TestPreviewTruncatesLongText(t *testing.T) {
	result := preview("abcdefghijklmnopqrstuvwxyz", 10)
	if result != "abcdefghij..." {
		t.Fatalf("unexpected preview: %q", result)
	}
}
