package app

import (
	"fmt"
	"io"
	"os"
	"strings"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/permission"
	"go-agent-harness/internal/workspace"
)

func newDefaultHooks(out io.Writer) *hooks.Manager {
	hookManager := hooks.NewManager()
	writer := outputWriter(out)
	// ----- UserPromptSubmit -----
	registerHook(hookManager, hooks.EventUserPromptSubmit, func(_ string) {
		_, _ = fmt.Fprintf(writer, "[HOOK] UserPromptSubmit: working in %s\n", workspace.Root())
	})

	// ----- PreToolUse -----
	registerHook(hookManager, hooks.EventPreToolUse, func(call hooks.ToolCall) string {
		if err := permission.Authorize(call.Name, call.Input); err != nil {
			return err.Error()
		}
		return ""
	})
	registerHook(hookManager, hooks.EventPreToolUse, func(call hooks.ToolCall) string {
		_, _ = fmt.Fprintf(writer, "[HOOK] %s(%s)\n", call.Name, previewInput(call.Input))
		return ""
	})

	// ----- PostToolUse -----
	registerHook(hookManager, hooks.EventPostToolUse, func(call hooks.ToolCall, output string) {
		if len(output) > 100000 {
			_, _ = fmt.Fprintf(writer, "[HOOK] Large output from %s: %d chars\n", call.Name, len(output))
		}
	})

	// ----- Stop -----
	registerHook(hookManager, hooks.EventStop, func(ctx hooks.StopContext) string {
		_, _ = fmt.Fprintf(writer, "[HOOK] Stop: session used %d tool calls\n", ctx.ToolCallCnt)
		return ""
	})
	return hookManager
}

func outputWriter(out io.Writer) io.Writer {
	if out != nil {
		return out
	}
	return os.Stdout
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
