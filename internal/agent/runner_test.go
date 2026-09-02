package agent

import (
	"strings"
	"testing"
)

func TestMessageHasToolUseInspectsBlocks(t *testing.T) {
	message := Message{Role: RoleAssistant, Blocks: []ContentBlock{
		{Type: BlockText, Text: "working"},
		{Type: BlockToolUse, ToolUseID: "tool-1", ToolName: "bash"},
	}}
	if !messageHasToolUse(message) {
		t.Fatal("expected actual tool_use block to drive loop continuation")
	}
	if messageHasToolUse(Message{Role: RoleAssistant, Blocks: []ContentBlock{{Type: BlockText}}}) {
		t.Fatal("empty/text-only response must not create a tool_result turn")
	}
}

func TestInjectBackgroundResultsPreservesUserContent(t *testing.T) {
	messages := []Message{{Role: RoleUser, Content: "next request"}}
	messages = injectBackgroundResults(messages, []string{"<task_notification>done</task_notification>"})
	if len(messages) != 1 || messages[0].Content != "" || len(messages[0].Blocks) != 2 {
		t.Fatalf("unexpected injected messages: %#v", messages)
	}
	if messages[0].Blocks[0].Text != "next request" || !strings.Contains(messages[0].Blocks[1].Text, "task_notification") {
		t.Fatalf("user content or notification was lost: %#v", messages[0].Blocks)
	}
}

func TestInjectBackgroundResultsAddsStandaloneUserEvent(t *testing.T) {
	messages := []Message{{Role: RoleAssistant, Content: "waiting"}}
	messages = injectBackgroundResults(messages, []string{"completed"})
	if len(messages) != 2 || messages[1].Role != RoleUser || len(messages[1].Blocks) != 1 || messages[1].Blocks[0].Text != "completed" {
		t.Fatalf("unexpected injected messages: %#v", messages)
	}
}

func TestExtractJSONArrayReturnsFirstValidArray(t *testing.T) {
	got := extractJSONArray(`prefix [invalid] text [{"name":"memory"}] suffix [1,2]`)
	if got != `[{"name":"memory"}]` {
		t.Fatalf("extractJSONArray() = %q", got)
	}
}
