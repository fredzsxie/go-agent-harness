package loop

import "go-agent-harness/internal/conversation"

type Role = conversation.Role

const (
	RoleUser      = conversation.RoleUser
	RoleAssistant = conversation.RoleAssistant
)

type BlockType = conversation.BlockType

const (
	BlockText       = conversation.BlockText
	BlockToolUse    = conversation.BlockToolUse
	BlockToolResult = conversation.BlockToolResult
)

type ContentBlock = conversation.ContentBlock

type Message = conversation.Message

// ToolSpec: Tool Specification
type ToolSpec struct {
	Name        string
	Description string
	Required    []string
	Properties  map[string]any
}

type RunResult struct {
	Messages []Message
	Output   string
}
