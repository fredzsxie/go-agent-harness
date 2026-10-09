package builtin

import (
	"go-agent-harness/internal/tool"
)

// RegisterTools 将本模块的模型输入 schema 与执行函数一起注册；装配层只负责选择能力。
func RegisterTools(registry *tool.Registry, tools *Tools, allowBackground bool) {
	// 后台参数只暴露给主 Agent；subagent 仍使用同步 Bash，避免声明未接入的能力。
	bashDescription := "Run a shell command in the current workspace. Prefer read_file/glob for inspection when possible."
	bashProperties := map[string]any{
		"command": map[string]any{"type": "string", "description": "Shell command to execute."},
	}
	if allowBackground {
		bashDescription = "Run a shell command in the current workspace. Set run_in_background to true only for an independent slow command."
		bashProperties["run_in_background"] = map[string]any{"type": "boolean", "description": "Run asynchronously and collect the result on a later turn."}
	}
	registry.Register(tool.Spec{
		Name:        "bash",
		Description: bashDescription,
		Required:    []string{"command"},
		Properties:  bashProperties,
	}, tools.RunBash)

	registry.Register(tool.Spec{
		Name:        "read_file",
		Description: "Read a UTF-8 text file inside the workspace.",
		Required:    []string{"path"},
		Properties: map[string]any{
			"path":  map[string]any{"type": "string", "description": "Workspace-relative file path."},
			"limit": map[string]any{"type": "integer", "minimum": 1, "description": "Optional maximum number of lines."},
		},
	}, tools.RunReadFile)
	registry.Register(tool.Spec{
		Name:        "write_file",
		Description: "Create or overwrite a file inside the workspace.",
		Required:    []string{"path", "content"},
		Properties: map[string]any{
			"path":    map[string]any{"type": "string", "description": "Workspace-relative file path."},
			"content": map[string]any{"type": "string", "description": "Full file content to write."},
		},
	}, tools.RunWriteFile)
	registry.Register(tool.Spec{
		Name:        "edit_file",
		Description: "Replace one exact text occurrence in a workspace file.",
		Required:    []string{"path", "old_text", "new_text"},
		Properties: map[string]any{
			"path":     map[string]any{"type": "string", "description": "Workspace-relative file path."},
			"old_text": map[string]any{"type": "string", "description": "Exact text to replace."},
			"new_text": map[string]any{"type": "string", "description": "Replacement text."},
		},
	}, tools.RunEditFile)
	registry.Register(tool.Spec{
		Name:        "glob",
		Description: "Find files by a glob pattern inside the workspace.",
		Required:    []string{"pattern"},
		Properties: map[string]any{
			"pattern": map[string]any{"type": "string", "description": "Glob pattern, for example **/*.go."},
		},
	}, tools.RunGlob)
}
