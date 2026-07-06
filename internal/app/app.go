package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"go-agent-harness/config"
	"go-agent-harness/internal/permission"
	"go-agent-harness/loop"
	"go-agent-harness/tools"
)

// REPL: Read-Eval-Print Loop
// ToolSpec: Tool Specification
type App struct {
	runner *loop.Runner
	in     io.Reader
	out    io.Writer
}

func New(cfg config.LLMConfig, in io.Reader, out io.Writer) *App {
	registry := newDefaultRegistry()
	registry.UseAuthorizer(permission.Authorize)

	return &App{
		runner: loop.NewRunner(cfg, registry),
		in:     in,
		out:    out,
	}
}

func (a *App) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(a.in)
	messages := make([]loop.Message, 0, 16)

	fmt.Fprintln(a.out, "go-agent-harness")
	fmt.Fprintln(a.out, "Type a task, or type q/exit to quit.")

	for {
		fmt.Fprint(a.out, "> ")
		if !scanner.Scan() {
			break
		}

		prompt := strings.TrimSpace(scanner.Text())
		if shouldExit(prompt) {
			break
		}
		if prompt == "" {
			continue
		}

		messages = append(messages, loop.Message{Role: loop.RoleUser, Content: prompt})
		output, err := a.runner.Run(ctx, messages)
		if err != nil {
			return err
		}
		if output != "" {
			fmt.Fprintln(a.out, output)
			messages = append(messages, loop.Message{Role: loop.RoleAssistant, Content: output})
		}
	}

	return scanner.Err()
}

func shouldExit(input string) bool {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "q", "quit", "exit":
		return true
	default:
		return false
	}
}

func newDefaultRegistry() *loop.Registry {
	registry := loop.NewRegistry()
	registry.Register(loop.ToolSpec{
		Name:        "bash",
		Description: "Run a shell command in the current workspace. Prefer read_file/glob for inspection when possible.",
		Required:    []string{"command"},
		Properties: map[string]any{
			"command": map[string]any{"type": "string", "description": "Shell command to execute."},
		},
	}, tools.RunBash)
	registry.Register(loop.ToolSpec{
		Name:        "read_file",
		Description: "Read a UTF-8 text file inside the workspace.",
		Required:    []string{"path"},
		Properties: map[string]any{
			"path": map[string]any{"type": "string", "description": "Workspace-relative file path."},
		},
	}, tools.RunReadFile)
	registry.Register(loop.ToolSpec{
		Name:        "write_file",
		Description: "Create or overwrite a file inside the workspace.",
		Required:    []string{"path", "content"},
		Properties: map[string]any{
			"path":    map[string]any{"type": "string", "description": "Workspace-relative file path."},
			"content": map[string]any{"type": "string", "description": "Full file content to write."},
		},
	}, tools.RunWriteFile)
	registry.Register(loop.ToolSpec{
		Name:        "edit_file",
		Description: "Replace one exact text occurrence in a workspace file.",
		Required:    []string{"path", "old_text", "new_text"},
		Properties: map[string]any{
			"path":     map[string]any{"type": "string", "description": "Workspace-relative file path."},
			"old_text": map[string]any{"type": "string", "description": "Exact text to replace."},
			"new_text": map[string]any{"type": "string", "description": "Replacement text."},
		},
	}, tools.RunEditFile)
	registry.Register(loop.ToolSpec{
		Name:        "glob",
		Description: "Find files by a glob pattern inside the workspace.",
		Required:    []string{"pattern"},
		Properties: map[string]any{
			"pattern": map[string]any{"type": "string", "description": "Glob pattern, for example **/*.go."},
		},
	}, tools.RunGlob)
	return registry
}
