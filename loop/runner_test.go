package loop

import "testing"

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
