package protocol_test

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
	if got := protocol.ActiveRequest(messages); got != "[Scheduled] first\n[Scheduled] second" {
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
	if got := protocol.ActiveRequest(messages); got != "run background checks" {
		t.Fatalf("unexpected reused request: %q", got)
	}
}

func TestCloneMessagesIsolatesNestedToolInput(t *testing.T) {
	messages := []protocol.Message{{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{
		Type: protocol.BlockToolUse, Input: map[string]any{"items": []any{map[string]any{"name": "original"}}},
	}}}}
	clone := protocol.CloneMessages(messages)
	clone[0].Blocks[0].Input["items"].([]any)[0].(map[string]any)["name"] = "changed"
	if messages[0].Blocks[0].Input["items"].([]any)[0].(map[string]any)["name"] != "original" {
		t.Fatal("nested input shared with clone")
	}
}

func TestTextHelpersPreferBlocksOverContentCache(t *testing.T) {
	message := protocol.Message{Role: protocol.RoleAssistant, Content: "stale cache", Blocks: []protocol.ContentBlock{
		{Type: protocol.BlockText, Text: "first"}, {Type: protocol.BlockToolResult, Text: "not user text"}, {Type: protocol.BlockText, Text: "second"},
	}}
	if got := protocol.LatestAssistantText([]protocol.Message{message}); got != "first\nsecond" {
		t.Fatalf("assistant text = %q", got)
	}
	message.Role = protocol.RoleUser
	if got := protocol.ActiveRequest([]protocol.Message{message}); got != "first\nsecond" {
		t.Fatalf("active request = %q", got)
	}
}
