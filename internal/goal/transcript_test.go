package goal

import (
	"strings"
	"testing"
	"unicode/utf8"

	"go-agent-harness/internal/protocol"
)

func TestTranscriptKeepsRecentCompleteMessages(t *testing.T) {
	messages := []protocol.Message{
		{Role: protocol.RoleUser, Content: "old"},
		{Role: protocol.RoleAssistant, Content: "middle"},
		{Role: protocol.RoleUser, Content: "new"},
	}
	middle := "ASSISTANT:\nmiddle"
	newest := "USER:\nnew"
	limit := utf8.RuneCountInString(middle) + 2 + utf8.RuneCountInString(newest)
	got := Transcript(messages, limit)
	if got != middle+"\n\n"+newest {
		t.Fatalf("unexpected transcript:\n%s", got)
	}
}

func TestTranscriptTrimsMiddleOfOversizedNewestMessage(t *testing.T) {
	content := "HEAD" + strings.Repeat("x", 100) + "TAIL"
	got := Transcript([]protocol.Message{{Role: protocol.RoleUser, Content: content}}, 40)
	if utf8.RuneCountInString(got) != 40 || !strings.Contains(got, omittedMarker) || !strings.Contains(got, "HEAD") || !strings.HasSuffix(got, "TAIL") {
		t.Fatalf("unexpected trimmed transcript (%d chars): %q", utf8.RuneCountInString(got), got)
	}
}

func TestTranscriptRendersToolEvidence(t *testing.T) {
	got := Transcript([]protocol.Message{
		{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{
			Type: protocol.BlockToolUse, ToolName: "bash", Input: map[string]any{"command": "go test ./..."},
		}}},
		{Role: protocol.RoleUser, Blocks: []protocol.ContentBlock{{
			Type: protocol.BlockToolResult, Text: "ok", IsError: true,
		}}},
	}, 1000)
	for _, want := range []string{`[tool_use bash {"command":"go test ./..."}]`, "[tool_result error ok]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("transcript missing %q:\n%s", want, got)
		}
	}
}
