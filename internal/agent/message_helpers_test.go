package agent

import (
	"testing"

	"go-agent-harness/internal/protocol"
)

func TestActiveRequestCollectsNewInputsAfterPreviousTurn(t *testing.T) {
	messages := []protocol.Message{
		{Role: protocol.RoleUser, Content: "old"},
		{Role: protocol.RoleAssistant, Content: "done"},
		{Role: protocol.RoleUser, Content: "[Scheduled] first"},
		{Role: protocol.RoleUser, Content: "[Scheduled] second"},
	}
	if got := ActiveRequest(messages); got != "[Scheduled] first\n[Scheduled] second" {
		t.Fatalf("unexpected active request: %q", got)
	}
}

func TestActiveRequestReusesLatestRequestForBackgroundWake(t *testing.T) {
	messages := []protocol.Message{
		{Role: protocol.RoleUser, Content: "run background checks"},
		{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{Type: protocol.BlockToolUse, ToolUseID: "bg", ToolName: "bash"}}},
		{Role: protocol.RoleUser, Blocks: []protocol.ContentBlock{{Type: protocol.BlockToolResult, ToolUseID: "bg", Text: "started"}}},
		{Role: protocol.RoleAssistant, Content: "waiting"},
	}
	if got := ActiveRequest(messages); got != "run background checks" {
		t.Fatalf("unexpected reused request: %q", got)
	}
}
