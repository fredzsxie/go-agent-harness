package app

import (
	"context"
	"fmt"
	"strings"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/mcp"
	"go-agent-harness/internal/permission"
	"go-agent-harness/internal/workspace"
)

func newDefaultHooks(mcpManager *mcp.Manager) *hooks.Manager {
	hookManager := hooks.NewManager()
	// ----- UserPromptSubmit -----
	hookManager.OnUserPrompt(func(_ string) {
		logger.Info("[HOOK] UserPromptSubmit: working in %s", workspace.Root())
	})

	// ----- PreToolUse -----
	hookManager.BeforeTool(func(ctx context.Context, call hooks.ToolCall) string {
		interactive := permission.IsInteractive(ctx)
		authorize := permission.Authorize
		if !interactive {
			authorize = permission.AuthorizeNonInteractive
		}
		if err := authorize(call.Name, call.Input); err != nil {
			logger.Warn("[Permission] denied %s: %v", call.Name, err)
			return err.Error()
		}
		// MCP annotations 由外部 Server 提供，只有 Host Policy 可以免除人工确认。
		if strings.HasPrefix(call.Name, "mcp__") && (mcpManager == nil || mcpManager.Policy(call.Name) != mcp.PolicyAllow) {
			logger.Warn("[MCP] Approval required for %s", call.Name)
			if err := permission.AuthorizeExternal(call.Name, call.Input, interactive); err != nil {
				return err.Error()
			}
		}
		return ""
	})
	hookManager.BeforeTool(func(_ context.Context, call hooks.ToolCall) string {
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
	hookManager.OnStop(func(_ context.Context, ctx hooks.StopContext) (hooks.StopDecision, error) {
		logger.Info("[HOOK] Stop: session used %d tool calls", ctx.ToolCallCnt)
		return hooks.StopDecision{Action: hooks.StopAllow}, nil
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
