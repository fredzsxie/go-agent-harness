package app

import (
	"fmt"
	"strings"
	"sync/atomic"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/permission"
	"go-agent-harness/internal/workspace"
)

func newDefaultHooks(nonInteractive *atomic.Bool) *hooks.Manager {
	hookManager := hooks.NewManager()
	// ----- UserPromptSubmit -----
	hookManager.OnUserPrompt(func(_ string) {
		logger.Info("[HOOK] UserPromptSubmit: working in %s", workspace.Root())
	})

	// ----- PreToolUse -----
	hookManager.BeforeTool(func(call hooks.ToolCall) string {
		authorize := permission.Authorize
		if nonInteractive != nil && nonInteractive.Load() {
			authorize = permission.AuthorizeNonInteractive
		}
		if err := authorize(call.Name, call.Input); err != nil {
			return err.Error()
		}
		return ""
	})
	hookManager.BeforeTool(func(call hooks.ToolCall) string {
		logger.Info("[HOOK] %s(%s)", call.Name, previewInput(call.Input))
		return ""
	})

	// ----- PostToolUse -----
	hookManager.AfterTool(func(call hooks.ToolCall, output string) {
		if len(output) > 100000 {
			logger.Info("[HOOK] Large output from %s: %d chars", call.Name, len(output))
		}
	})

	// ----- Stop -----
	hookManager.OnStop(func(ctx hooks.StopContext) string {
		logger.Info("[HOOK] Stop: session used %d tool calls", ctx.ToolCallCnt)
		return ""
	})
	return hookManager
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
