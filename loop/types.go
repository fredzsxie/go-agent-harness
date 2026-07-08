package loop

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
	Type      BlockType
	Text      string
	ToolUseID string
	ToolName  string
	Input     map[string]any
	IsError   bool
}

type Message struct {
	Role    Role
	Content string
	Blocks  []ContentBlock
}

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
