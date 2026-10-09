package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/protocol"
)

type workspaceModel struct {
	requests []llm.Request
}

func (m *workspaceModel) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	// 无工具的请求属于 Memory 辅助调用；不把它与主 Agent 的两轮交互混在一起。
	if len(request.Tools) == 0 {
		return llm.Response{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "[]"}}, nil
	}
	request.Messages = protocol.CloneMessages(request.Messages)
	m.requests = append(m.requests, request)
	switch len(m.requests) {
	case 1:
		return llm.Response{Message: protocol.Message{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{
			{Type: protocol.BlockToolUse, ToolUseID: "pwd", ToolName: "bash", Input: map[string]any{"command": "pwd"}},
			{Type: protocol.BlockToolUse, ToolUseID: "read", ToolName: "read_file", Input: map[string]any{"path": "note.txt"}},
		}}}, nil
	case 2:
		return llm.Response{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "verified"}}, nil
	default:
		return llm.Response{}, fmt.Errorf("approval answer was consumed as a new user task")
	}
}

func TestAppBindsWorkspaceAndSharesApprovalInput(t *testing.T) {
	for _, choice := range []string{"y", "n"} {
		t.Run(choice, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("workspace "+choice), 0o600); err != nil {
				t.Fatal(err)
			}
			model := &workspaceModel{}
			output := &lockedBuffer{}
			application, err := NewWithConfig(Config{
				WorkDir: root, Model: model, In: strings.NewReader("inspect\n" + choice + "\nexit\n"), Out: output,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := application.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(model.requests) != 2 {
				t.Fatalf("unexpected main turns: %d", len(model.requests))
			}
			messages := model.requests[1].Messages
			blocks := messages[len(messages)-1].Blocks
			if len(blocks) != 2 || blocks[1].Text != "workspace "+choice {
				t.Fatalf("tools used wrong workspace: %#v", blocks)
			}
			if choice == "y" {
				canonical, err := filepath.EvalSymlinks(root)
				if err != nil {
					t.Fatal(err)
				}
				if blocks[0].IsError || strings.TrimSpace(blocks[0].Text) != canonical {
					t.Fatalf("bash cwd = %#v; want %s", blocks[0], canonical)
				}
			} else if !blocks[0].IsError {
				t.Fatal("denied command executed")
			}
			if !strings.Contains(output.String(), "Allow? [y/N]") || !strings.Contains(output.String(), "verified") {
				t.Fatalf("approval/result missing from configured output: %s", output.String())
			}
		})
	}
}

func TestAppRejectsMissingStreams(t *testing.T) {
	if _, err := NewWithConfig(Config{WorkDir: t.TempDir(), Model: &workspaceModel{}}); err == nil {
		t.Fatal("missing streams must fail during construction")
	}
}
