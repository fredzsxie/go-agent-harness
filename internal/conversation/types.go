// Package conversation defines the shared message protocol used by the agent
// loop and context-management components.
package conversation

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type BlockType string

const (
	BlockText       BlockType = "text"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
)

type ContentBlock struct {
	Type      BlockType      `json:"type"`
	Text      string         `json:"text,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

type Message struct {
	Role    Role           `json:"role"`
	Content string         `json:"content,omitempty"`
	Blocks  []ContentBlock `json:"blocks,omitempty"`
}
