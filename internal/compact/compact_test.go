package compact

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestSnipCompactPreservesToolUseResultPairAtTail(t *testing.T) {
	manager := New(Config{MaxMessages: 6})
	messages := []Message{
		{Role: RoleUser, Content: "start"},
		{Role: RoleUser, Content: "one"},
		{Role: RoleUser, Content: "two"},
		{Role: RoleUser, Content: "three"},
		{Role: RoleAssistant, Blocks: []ContentBlock{{Type: BlockToolUse, ToolUseID: "tool-1", ToolName: "read_file"}}},
		{Role: RoleUser, Blocks: []ContentBlock{{Type: BlockToolResult, ToolUseID: "tool-1", Text: "result"}}},
		{Role: RoleAssistant, Content: "done"},
	}

	compacted := manager.SnipCompact(messages)

	if len(compacted) != 7 {
		t.Fatalf("expected tail tool pair to be preserved with placeholder, got %d messages", len(compacted))
	}
	if compacted[3].Role != RoleUser || !strings.Contains(compacted[3].Content, "snipped") {
		t.Fatalf("expected snip placeholder at index 3, got %#v", compacted[3])
	}
	if !messageHasToolUse(compacted[4]) || !isToolResultMessage(compacted[5]) {
		t.Fatalf("expected tool_use/tool_result pair to remain adjacent near tail: %#v", compacted)
	}
}

func TestMicroCompactKeepsRecentToolResults(t *testing.T) {
	manager := New(Config{KeepRecentToolResults: 2})
	messages := []Message{
		toolResultMessage("a", strings.Repeat("a", 130)),
		toolResultMessage("b", strings.Repeat("b", 130)),
		toolResultMessage("c", strings.Repeat("c", 130)),
	}

	compacted := manager.MicroCompact(messages)

	if compacted[0].Blocks[0].Text != "[Earlier tool result compacted. Re-run if needed.]" {
		t.Fatalf("expected oldest result to be compacted, got %q", compacted[0].Blocks[0].Text)
	}
	if compacted[1].Blocks[0].Text != messages[1].Blocks[0].Text || compacted[2].Blocks[0].Text != messages[2].Blocks[0].Text {
		t.Fatalf("expected two recent results to remain unchanged")
	}
}

func TestToolResultBudgetPersistsLargeOutput(t *testing.T) {
	dir := t.TempDir()
	manager := New(Config{
		WorkDir:          dir,
		ToolResultBudget: 10,
		PersistThreshold: 10,
		PreviewBytes:     5,
	})
	messages := []Message{
		{Role: RoleUser, Blocks: []ContentBlock{{
			Type:      BlockToolResult,
			ToolUseID: "tool-1",
			Text:      "0123456789abcdefghijklmnopqrstuvwxyz",
		}}},
	}

	compacted, err := manager.ToolResultBudget(messages)
	if err != nil {
		t.Fatal(err)
	}

	text := compacted[0].Blocks[0].Text
	if !strings.Contains(text, "<persisted-output>") || !strings.Contains(text, "Preview:\n01234") {
		t.Fatalf("expected persisted output marker with preview, got %q", text)
	}
	if _, err := os.Stat(dir + "/.task_outputs/tool-results/tool-1.txt"); err != nil {
		t.Fatalf("expected persisted tool result file: %v", err)
	}
}

func TestPrepareAutoCompactsWhenOverLimit(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir(), ContextLimit: 20})
	messages := []Message{{Role: RoleUser, Content: strings.Repeat("x", 100)}}

	compacted, didCompact, err := manager.Prepare(context.Background(), messages, func(context.Context, []Message) (string, error) {
		return "summary", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !didCompact {
		t.Fatal("expected auto compact")
	}
	if len(compacted) != 1 || compacted[0].Content != "[Compacted]\n\nsummary" {
		t.Fatalf("unexpected compacted message: %#v", compacted)
	}
}

func toolResultMessage(id string, text string) Message {
	return Message{Role: RoleUser, Blocks: []ContentBlock{{Type: BlockToolResult, ToolUseID: id, Text: text}}}
}
