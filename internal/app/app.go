package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"go-agent-harness/config"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/permission"
	"go-agent-harness/internal/todo"
	"go-agent-harness/loop"
	"go-agent-harness/tools"
)

type App struct {
	runner *loop.Runner
	in     io.Reader
	out    io.Writer
}

func New(cfg config.LLMConfig, in io.Reader, out io.Writer) *App {
	registry := newDefaultRegistry()
	hookManager := newDefaultHooks()

	// add todo_write tool_use seperately
	todoManager := todo.NewManager(out)
	registerTodoTool(registry, todoManager)

	return &App{
		runner: loop.NewRunner(cfg, registry, hookManager),
		in:     in,
		out:    out,
	}
}

func (a *App) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(a.in)
	messages := make([]loop.Message, 0, 16)

	fmt.Fprintln(a.out, "go-agent-harness")
	fmt.Fprintln(a.out, "Type a task, or type q/exit to quit.")

	// REPL: Read-Eval-Print Loop
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
		result, err := a.runner.Run(ctx, messages)
		if err != nil {
			return err
		}
		messages = result.Messages
		if result.Output != "" {
			fmt.Fprintln(a.out, result.Output)
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

func registerTodoTool(registry *loop.Registry, manager *todo.Manager) {
	registry.Register(loop.ToolSpec{
		Name:        "todo_write",
		Description: "Create and manage a task list for your current coding session. Use this before and during multi-step work.",
		Required:    []string{"todos"},
		Properties: map[string]any{
			"todos": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"content": map[string]any{"type": "string"},
						"status": map[string]any{
							"type": "string",
							"enum": []string{"pending", "in_progress", "completed"},
						},
					},
					"required": []string{"content", "status"},
				},
			},
		},
	}, manager.RunWrite)
}

func newDefaultHooks() *hooks.Manager {
	hookManager := hooks.NewManager()
	// ----- UserPromptSubmit -----
	registerHook(hookManager, hooks.EventUserPromptSubmit, func(query string) {
		cwd, err := os.Getwd()
		if err != nil {
			cwd = "."
		}
		fmt.Printf("[HOOK] UserPromptSubmit: working in %s\n", cwd)
	})

	// ----- PreToolUse -----
	registerHook(hookManager, hooks.EventPreToolUse, func(call hooks.ToolCall) string {
		if err := permission.Authorize(call.Name, call.Input); err != nil {
			return err.Error()
		}
		return ""
	})
	registerHook(hookManager, hooks.EventPreToolUse, func(call hooks.ToolCall) string {
		fmt.Printf("[HOOK] %s(%s)\n", call.Name, previewInput(call.Input))
		return ""
	})

	// ----- PostToolUse -----
	registerHook(hookManager, hooks.EventPostToolUse, func(call hooks.ToolCall, output string) {
		if len(output) > 100000 {
			fmt.Printf("[HOOK] Large output from %s: %d chars\n", call.Name, len(output))
		} else {
			// FOR DEBUG
			// fmt.Printf("[HOOK] %s output: %s\n", call.Name, output)
		}
	})

	// ----- Stop -----
	registerHook(hookManager, hooks.EventStop, func(ctx hooks.StopContext) string {
		fmt.Printf("[HOOK] Stop: session used %d tool calls\n", ctx.ToolCallCnt)
		return ""
	})
	return hookManager
}

func registerHook(manager *hooks.Manager, event hooks.Event, callback any) {
	if err := manager.Register(event, callback); err != nil {
		panic(err)
	}
}

func previewInput(input map[string]any) string {
	const maxLen = 60
	parts := make([]string, 0, 2)
	for key, value := range input {
		parts = append(parts, fmt.Sprintf("%s=%v", key, value))
		if len(parts) == 2 {
			break
		}
	}
	preview := strings.Join(parts, ", ")
	if len(preview) > maxLen {
		return preview[:maxLen] + "..."
	}
	return preview
}
