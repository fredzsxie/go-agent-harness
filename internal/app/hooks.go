package app

import (
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/logging"
	"go-agent-harness/internal/permission"
	"go-agent-harness/internal/workspace"
)

func newDefaultHooks(out io.Writer, nonInteractive *atomic.Bool) *hooks.Manager {
	hookManager := hooks.NewManager()
	// ----- UserPromptSubmit -----
	registerHook(hookManager, hooks.EventUserPromptSubmit, func(_ string) {
		logging.Printf("[HOOK] UserPromptSubmit: working in %s", workspace.Root())
	})

	// ----- PreToolUse -----
	registerHook(hookManager, hooks.EventPreToolUse, func(call hooks.ToolCall) string {
		authorize := permission.Authorize
		if nonInteractive != nil && nonInteractive.Load() {
			authorize = permission.AuthorizeNonInteractive
		}
		if err := authorize(call.Name, call.Input); err != nil {
			return err.Error()
		}
		return ""
	})
	registerHook(hookManager, hooks.EventPreToolUse, func(call hooks.ToolCall) string {
		logging.Printf("[HOOK] %s(%s)", call.Name, previewInput(call.Input))
		return ""
	})

	// ----- PostToolUse -----
	registerHook(hookManager, hooks.EventPostToolUse, func(call hooks.ToolCall, output string) {
		if len(output) > 100000 {
			logging.Printf("[HOOK] Large output from %s: %d chars", call.Name, len(output))
		}
	})

	// ----- Stop -----
	registerHook(hookManager, hooks.EventStop, func(ctx hooks.StopContext) string {
		logging.Printf("[HOOK] Stop: session used %d tool calls", ctx.ToolCallCnt)
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
