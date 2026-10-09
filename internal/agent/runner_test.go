package agent

import (
	"strings"
	"testing"

	"go-agent-harness/internal/protocol"
)

func TestMessageHasToolUseInspectsBlocks(t *testing.T) {
	message := protocol.Message{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{
		{Type: protocol.BlockText, Text: "working"},
		{Type: protocol.BlockToolUse, ToolUseID: "tool-1", ToolName: "bash"},
	}}
	if !messageHasToolUse(message) {
		t.Fatal("expected actual tool_use block to drive loop continuation")
	}
	if messageHasToolUse(protocol.Message{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{Type: protocol.BlockText}}}) {
		t.Fatal("empty/text-only response must not create a tool_result turn")
	}
}

func TestInjectBackgroundResultsPreservesUserContent(t *testing.T) {
	messages := []protocol.Message{{Role: protocol.RoleUser, Content: "next request"}}
	messages = injectBackgroundResults(messages, []string{"<task_notification>done</task_notification>"})
	if len(messages) != 1 || messages[0].Content != "" || len(messages[0].Blocks) != 2 {
		t.Fatalf("unexpected injected messages: %#v", messages)
	}
	if messages[0].Blocks[0].Text != "next request" || !strings.Contains(messages[0].Blocks[1].Text, "task_notification") {
		t.Fatalf("user content or notification was lost: %#v", messages[0].Blocks)
	}
}

func TestInjectBackgroundResultsAddsStandaloneUserEvent(t *testing.T) {
	messages := []protocol.Message{{Role: protocol.RoleAssistant, Content: "waiting"}}
	messages = injectBackgroundResults(messages, []string{"completed"})
	if len(messages) != 2 || messages[1].Role != protocol.RoleUser || len(messages[1].Blocks) != 1 || messages[1].Blocks[0].Text != "completed" {
		t.Fatalf("unexpected injected messages: %#v", messages)
	}
}

func TestBackgroundNotificationFollowsAuthoritativeToolResults(t *testing.T) {
	messages := []protocol.Message{{Role: protocol.RoleUser, Content: "stale cache", Blocks: []protocol.ContentBlock{
		{Type: protocol.BlockToolResult, ToolUseID: "read", Text: "result"},
	}}}
	messages = injectBackgroundResults(messages, []string{"background done"})
	blocks := messages[0].Blocks
	if messages[0].Content != "" || len(blocks) != 2 || blocks[0].Type != protocol.BlockToolResult || blocks[1].Text != "background done" {
		t.Fatalf("notification reordered results or duplicated Content: %#v", messages)
	}
}
