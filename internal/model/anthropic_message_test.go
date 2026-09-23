package model

import (
	"encoding/json"
	"strings"
	"testing"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"go-agent-harness/internal/protocol"
)

func TestParseModelResponsePreservesTokenUsage(t *testing.T) {
	var response anthropic.Message
	if err := json.Unmarshal([]byte(`{
		"id":"msg_01",
		"type":"message",
		"role":"assistant",
		"model":"claude-test",
		"content":[{"type":"text","text":"done"}],
		"stop_reason":"end_turn",
		"stop_sequence":null,
		"usage":{"input_tokens":12,"output_tokens":7}
	}`), &response); err != nil {
		t.Fatal(err)
	}

	result := parseModelResponse(&response)
	if result.Message.Content != "done" || result.StopReason != "end_turn" {
		t.Fatalf("unexpected model response: %#v", result)
	}
	if result.Usage.InputTokens != 12 || result.Usage.OutputTokens != 7 {
		t.Fatalf("unexpected token usage: %#v", result.Usage)
	}
}

func TestToAnthropicMessagesPreservesToolProtocol(t *testing.T) {
	messages := []protocol.Message{
		{Role: protocol.RoleUser, Content: "读取文件"},
		{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{
			Type: protocol.BlockToolUse, ToolUseID: "toolu_01", ToolName: "read_file",
			Input: map[string]any{"path": "README.md"},
		}}},
		{Role: protocol.RoleUser, Blocks: []protocol.ContentBlock{{
			Type: protocol.BlockToolResult, ToolUseID: "toolu_01", Text: "content",
		}}},
	}

	raw, err := json.Marshal(toAnthropicMessages(messages))
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	for _, expected := range []string{`"role":"user"`, `"type":"tool_use"`, `"id":"toolu_01"`, `"type":"tool_result"`, `"tool_use_id":"toolu_01"`} {
		if !strings.Contains(encoded, expected) {
			t.Fatalf("encoded messages missing %s: %s", expected, encoded)
		}
	}
}

func TestParseAssistantMessageBuildsInternalBlocks(t *testing.T) {
	var content []anthropic.ContentBlockUnion
	if err := json.Unmarshal([]byte(`[
		{"type":"text","text":"我先读取文件。"},
		{"type":"tool_use","id":"toolu_01","name":"read_file","input":{"path":"README.md"}}
	]`), &content); err != nil {
		t.Fatal(err)
	}

	message := parseAssistantMessage(content)
	if message.Role != protocol.RoleAssistant || message.Content != "我先读取文件。" {
		t.Fatalf("unexpected message: %#v", message)
	}
	if len(message.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(message.Blocks))
	}
	toolUse := message.Blocks[1]
	if toolUse.Type != protocol.BlockToolUse || toolUse.ToolUseID != "toolu_01" || toolUse.ToolName != "read_file" {
		t.Fatalf("unexpected tool_use block: %#v", toolUse)
	}
	if toolUse.Input["path"] != "README.md" {
		t.Fatalf("unexpected tool input: %#v", toolUse.Input)
	}
}
