package loop

import "go-agent-harness/internal/agent"

type Role = agent.Role

const (
	RoleUser      = agent.RoleUser
	RoleAssistant = agent.RoleAssistant
)

type BlockType = agent.BlockType

const (
	BlockText       = agent.BlockText
	BlockToolUse    = agent.BlockToolUse
	BlockToolResult = agent.BlockToolResult
)

type ContentBlock = agent.ContentBlock

type Message = agent.Message

type ToolSpec = agent.ToolSpec

type RunResult struct {
	Messages []Message
	Output   string
}
