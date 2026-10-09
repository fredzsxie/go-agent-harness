package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/prompt"
	"go-agent-harness/internal/protocol"
	agentruntime "go-agent-harness/internal/runtime"
	"go-agent-harness/internal/tool"
)

// 本文件连接 s11 后台通知和 s08 会话控制工具；普通工具始终通过 Registry 执行。
func newBackgroundManager(registry *tool.Registry) *agentruntime.BackgroundManager {
	// 后台执行仍复用 tool.Registry 中的 Bash handler，避免维护第二套命令执行逻辑。
	return agentruntime.NewBackground(func(ctx context.Context, command string) (string, error) {
		return registry.Dispatch(ctx, "bash", map[string]any{"command": command})
	})
}

func (r *Runner) Close() {
	// Runner 的生命周期结束时同步回收尚未完成的后台命令。
	if r.background != nil {
		r.background.Close()
	}
}

// BackgroundReady 向 App 暴露后台任务完成信号。
func (r *Runner) BackgroundReady() <-chan struct{} {
	return r.background.Ready()
}

// HasBackgroundResults 判断是否仍有未注入会话的后台结果。
func (r *Runner) HasBackgroundResults() bool {
	return r.background.HasReady()
}

// HasBackgroundWork 判断是否存在尚未产出最终结果的后台命令。
func (r *Runner) HasBackgroundWork() bool {
	return r.background.HasRunning()
}

func enabledToolNames(specs []tool.Spec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

func compactToolResultText(results []protocol.ContentBlock) string {
	if len(results) == 0 || strings.TrimSpace(results[0].Text) == "" {
		return "[Compacted. Conversation history has been summarized.]"
	}
	return results[0].Text
}

func (r *Runner) interceptTool(ctx context.Context, sessionMessages []protocol.Message, call hooks.ToolCall, activeRequest string) (ToolOutcome, bool, error) {
	if call.Name == "compact" {
		compacted, err := r.context.Compact(ctx, sessionMessages, activeRequest)
		if err != nil {
			return ToolOutcome{}, true, err
		}
		return ToolOutcome{
			Text:     "[Compacted. Conversation history has been summarized.]",
			Stop:     true,
			Messages: compacted,
		}, true, nil
	}

	if agentruntime.ShouldRunBackground(call.Name, call.Input) {
		// PreToolUse 已通过后才异步启动，并立即用占位结果结束本次 tool_use。
		command, _ := call.Input["command"].(string)
		id, err := r.background.Start(command, func(output string) {
			r.hooks.TriggerPostToolUse(call, output)
		})
		if err != nil {
			return ToolOutcome{Text: "Error: " + err.Error(), IsError: true}, true, nil
		}
		return ToolOutcome{
			Text:     fmt.Sprintf("[Background task %s started] The result will be collected on a later turn.", id),
			SkipPost: true,
		}, true, nil
	}
	return ToolOutcome{}, false, nil
}

func (r *Runner) currentLiveContext() prompt.LiveContext {
	live := prompt.LiveContext{}
	if r.liveContext != nil {
		live = r.liveContext()
	}
	if strings.TrimSpace(live.CurrentTime) == "" {
		live.CurrentTime = time.Now().Format(time.RFC3339)
	}
	return live
}

func injectBackgroundResults(messages []protocol.Message, notifications []string) []protocol.Message {
	if len(notifications) == 0 {
		return messages
	}
	blocks := make([]protocol.ContentBlock, 0, len(notifications)+1)
	for _, notification := range notifications {
		blocks = append(blocks, protocol.ContentBlock{Type: protocol.BlockText, Text: notification})
	}
	if len(messages) == 0 || messages[len(messages)-1].Role != protocol.RoleUser {
		// assistant 结尾时新增 user 通知回合，不制造重复的 tool_result。
		return append(messages, protocol.Message{Role: protocol.RoleUser, Blocks: blocks})
	}

	last := &messages[len(messages)-1]
	// user 结尾时合并到原回合，并保留已有文本或 tool_result 的先后顺序。
	if len(last.Blocks) == 0 && strings.TrimSpace(last.Content) != "" {
		last.Blocks = []protocol.ContentBlock{{Type: protocol.BlockText, Text: last.Content}}
	}
	// Blocks 是权威内容；已有 tool_result 时丢弃 Content 缓存，通知始终排在结果后。
	last.Content = ""
	last.Blocks = append(last.Blocks, blocks...)
	return messages
}

func messageHasToolUse(message protocol.Message) bool {
	for _, block := range message.Blocks {
		if block.Type == protocol.BlockToolUse {
			return true
		}
	}
	return false
}

func messageUsesTool(message protocol.Message, name string) bool {
	for _, block := range message.Blocks {
		if block.Type == protocol.BlockToolUse && block.ToolName == name {
			return true
		}
	}
	return false
}

func isPromptTooLong(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "prompt_too_long") ||
		(strings.Contains(text, "prompt") && strings.Contains(text, "long")) ||
		strings.Contains(text, "too many tokens") ||
		strings.Contains(text, "context length") ||
		strings.Contains(text, "context_length_exceeded") ||
		strings.Contains(text, "context window") ||
		strings.Contains(text, "max_context_window")
}
