package loop

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Message struct {
	Role      Role
	Content   string
	ToolUseID string
	ToolName  string
}

type ToolSpec struct {
	Name        string
	Description string
	Required    []string
	Properties  map[string]any
}
