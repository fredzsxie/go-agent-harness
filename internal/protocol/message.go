// Package protocol 定义 Agent 各组件共享的消息协议。
package protocol

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

// ContentBlock 表示一段结构化消息内容。
// 当前项目仅支持 Anthropic 客户端工具协议需要的三种 block。
type ContentBlock struct {
	Type      BlockType      `json:"type"`
	Text      string         `json:"text,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	IsError   bool           `json:"is_error,omitempty"`
}

// Message 表示一个 user 或 assistant 对话回合。
// Content 是纯文本的便捷表示；Blocks 非空时以 Blocks 为准。
type Message struct {
	Role    Role           `json:"role"`
	Content string         `json:"content,omitempty"`
	Blocks  []ContentBlock `json:"blocks,omitempty"`
}
