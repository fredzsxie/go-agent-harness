package agent

import "go-agent-harness/internal/protocol"

type Role = protocol.Role

const (
	RoleUser      = protocol.RoleUser
	RoleAssistant = protocol.RoleAssistant
)

type BlockType = protocol.BlockType

const (
	BlockText       = protocol.BlockText
	BlockToolUse    = protocol.BlockToolUse
	BlockToolResult = protocol.BlockToolResult
)

type ContentBlock = protocol.ContentBlock
type Message = protocol.Message
