package compact

import (
	"context"
	"os"
	"strings"
	"testing"

	"go-agent-harness/internal/protocol"
)

func TestSnipCompactPreservesToolUseResultPairAtTail(t *testing.T) {
	manager := New(Config{MaxMessages: 6})
	messages := []protocol.Message{
		{Role: protocol.RoleUser, Content: "start"},
		{Role: protocol.RoleUser, Content: "one"},
		{Role: protocol.RoleUser, Content: "two"},
		{Role: protocol.RoleUser, Content: "three"},
		{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{Type: protocol.BlockToolUse, ToolUseID: "tool-1", ToolName: "read_file"}}},
		{Role: protocol.RoleUser, Blocks: []protocol.ContentBlock{{Type: protocol.BlockToolResult, ToolUseID: "tool-1", Text: "result"}}},
		{Role: protocol.RoleAssistant, Content: "done"},
	}

	compacted := manager.SnipCompact(messages)

	if len(compacted) != 7 {
		t.Fatalf("expected tail tool pair to be preserved with placeholder, got %d messages", len(compacted))
	}
	if compacted[3].Role != protocol.RoleUser || !strings.Contains(compacted[3].Content, "snipped") {
		t.Fatalf("expected snip placeholder at index 3, got %#v", compacted[3])
	}
	if !messageHasToolUse(compacted[4]) || !isToolResultMessage(compacted[5]) {
		t.Fatalf("expected tool_use/tool_result pair to remain adjacent near tail: %#v", compacted)
	}
}

func TestMicroCompactKeepsRecentToolResults(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir(), KeepRecentToolResults: 2})
	messages := []protocol.Message{
		toolResultMessage("a", strings.Repeat("a", 130)),
		toolResultMessage("b", strings.Repeat("b", 130)),
		toolResultMessage("c", strings.Repeat("c", 130)),
	}

	compacted := manager.MicroCompact(messages)

	if !strings.HasPrefix(compacted[0].Blocks[0].Text, "[Earlier tool result saved at ") {
		t.Fatalf("expected oldest result to be persisted, got %q", compacted[0].Blocks[0].Text)
	}
	if compacted[1].Blocks[0].Text != messages[1].Blocks[0].Text || compacted[2].Blocks[0].Text != messages[2].Blocks[0].Text {
		t.Fatalf("expected two recent results to remain unchanged")
	}
}

func TestPreparePreservesToolResultsBelowContextLimit(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir(), ContextLimit: 10000, KeepRecentToolResults: 1})
	messages := []protocol.Message{
		toolResultMessage("a", strings.Repeat("a", 130)),
		toolResultMessage("b", strings.Repeat("b", 130)),
		toolResultMessage("c", strings.Repeat("c", 130)),
	}
	prepared, didCompact, err := manager.Prepare(context.Background(), messages, nil)
	if err != nil {
		t.Fatal(err)
	}
	if didCompact || prepared[0].Blocks[0].Text != messages[0].Blocks[0].Text {
		t.Fatalf("tool results changed without context pressure: %#v", prepared)
	}
}

func TestMicroCompactPreservesWholeUnseenToolResultBatch(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir(), KeepRecentToolResults: 1})
	messages := []protocol.Message{
		{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{Type: protocol.BlockToolUse, ToolUseID: "old", ToolName: "bash"}}},
		toolResultMessage("old", strings.Repeat("o", 130)),
		{Role: protocol.RoleAssistant, Content: "observed"},
		{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{
			{Type: protocol.BlockToolUse, ToolUseID: "new-1", ToolName: "bash"},
			{Type: protocol.BlockToolUse, ToolUseID: "new-2", ToolName: "bash"},
		}},
		{Role: protocol.RoleUser, Blocks: []protocol.ContentBlock{
			{Type: protocol.BlockToolResult, ToolUseID: "new-1", Text: strings.Repeat("1", 130)},
			{Type: protocol.BlockToolResult, ToolUseID: "new-2", Text: strings.Repeat("2", 130)},
		}},
		{Role: protocol.RoleUser, Content: "<reminder>Update your todos.</reminder>"},
	}

	compacted := manager.MicroCompact(messages)
	if compacted[4].Blocks[0].Text != messages[4].Blocks[0].Text || compacted[4].Blocks[1].Text != messages[4].Blocks[1].Text {
		t.Fatalf("unseen tool result batch was compacted: %#v", compacted[4])
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
	messages := []protocol.Message{
		{Role: protocol.RoleUser, Blocks: []protocol.ContentBlock{{
			Type:      protocol.BlockToolResult,
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

func TestToolResultBudgetFindsUnseenResultBeforeTrailingNotice(t *testing.T) {
	dir := t.TempDir()
	manager := New(Config{WorkDir: dir, PersistThreshold: 10, ToolResultBudget: 1000, PreviewBytes: 5})
	messages := []protocol.Message{
		{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{Type: protocol.BlockToolUse, ToolUseID: "tool-1", ToolName: "read_file"}}},
		toolResultMessage("tool-1", "0123456789abcdefghijklmnopqrstuvwxyz"),
		{Role: protocol.RoleUser, Content: "<reminder>Update your todos.</reminder>"},
	}

	prepared, err := manager.ToolResultBudget(messages)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepared[1].Blocks[0].Text, "<persisted-output>") {
		t.Fatalf("unseen large result before notice was not persisted: %#v", prepared)
	}
}

func TestPrepareAutoCompactsWhenOverLimit(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir(), ContextLimit: 20})
	messages := []protocol.Message{{Role: protocol.RoleUser, Content: strings.Repeat("x", 100)}}

	compacted, didCompact, err := manager.Prepare(context.Background(), messages, func(context.Context, []protocol.Message) (string, error) {
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

func toolResultMessage(id string, text string) protocol.Message {
	return protocol.Message{Role: protocol.RoleUser, Blocks: []protocol.ContentBlock{{Type: protocol.BlockToolResult, ToolUseID: id, Text: text}}}
}
