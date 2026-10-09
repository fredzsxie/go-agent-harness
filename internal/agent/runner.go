// Package agent 实现 s01 的核心循环，并接入 s15 的上下文、工具与运行时机制。
package agent

import (
	"context"
	"strings"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/prompt"
	"go-agent-harness/internal/protocol"
)

// Run 在一条消息链上反复执行“准备上下文 → 模型 → 工具 → 回填结果”。
// 无工具调用只代表模型希望结束；s04/s17 的 Stop hook 仍可要求继续。
func (r *Runner) Run(ctx context.Context, messages []protocol.Message) (RunResult, error) {
	sessionMessages := protocol.CloneMessages(messages)
	tokenBaseline := tokenBaselineFromContext(ctx)
	activeRequest, configured := activeRequestFromContext(ctx)
	if !configured {
		activeRequest = protocol.ActiveRequest(sessionMessages)
	}

	if userPrompt := protocol.LatestUserPrompt(sessionMessages); userPrompt != "" {
		r.hooks.TriggerUserPromptSubmit(userPrompt)
	}

	// s09：每次请求只召回一次相关记忆；后续轮次刷新动态状态时复用召回结果。
	systemPrompt, err := r.context.StartRequest(ctx, sessionMessages, enabledToolNames(r.registry.Specs()), r.currentLiveContext())
	if err != nil {
		return RunResult{}, err
	}

	toolCallCnt := 0
	turnCount := 0
	var usage llm.Usage
	reactiveRetries := 0
	recoveryState := recoveryState{fallbackModel: r.fallbackModel}
	maxTokens := DefaultMaxTokens
	hasEscalated := false
	recoveryCount := 0
	roundsSinceTodo := 0
	extractionSource := protocol.CloneMessages(sessionMessages)
	for {
		// 每轮刷新工具、MCP、Teammate 和时间等实时 Prompt 上下文。
		systemPrompt = r.context.RefreshPrompt(enabledToolNames(r.registry.Specs()), r.currentLiveContext())
		extractionSource = protocol.CloneMessages(sessionMessages)
		// 自动运行时会唤醒 Agent，结果仍在此统一注入消息历史。
		sessionMessages = injectBackgroundResults(sessionMessages, r.background.Collect())
		if r.maxTurns > 0 && turnCount >= r.maxTurns {
			reason := "global max_turns reached; any active goal remains active"
			logger.Warn("[Goal] %s (%d)", reason, r.maxTurns)
			return RunResult{
				Messages: sessionMessages, Usage: usage,
				Stop: hooks.StopDecision{Action: hooks.StopLimit, Reason: reason},
			}, nil
		}

		// s08：每次调用前检查预算，优先处理可恢复的工具输出，超限时才调用模型摘要。
		prepared, err := r.context.Prepare(ctx, sessionMessages, activeRequest)
		if err != nil {
			return RunResult{Messages: sessionMessages, Usage: usage}, err
		}
		sessionMessages = prepared

		turn, err := r.runTurnWithRetry(ctx, &recoveryState, systemPrompt, sessionMessages, maxTokens, func(ctx context.Context, messages []protocol.Message, call hooks.ToolCall) (ToolOutcome, bool, error) {
			return r.interceptTool(ctx, messages, call, activeRequest)
		})
		usage.Add(turn.Usage)
		if turn.Assistant.Role != "" {
			turnCount++
		}
		logger.Info("[LLM] Main LLM calling done.")
		if err != nil {
			// 已产生 assistant 消息说明错误来自工具阶段，不应按模型上下文超限重试。
			if turn.Assistant.Role != "" {
				return RunResult{Messages: sessionMessages, Usage: usage}, err
			}
			if isPromptTooLong(err) && reactiveRetries < r.context.MaxReactiveRetries() {
				compacted, compactErr := r.context.ReactiveCompact(ctx, sessionMessages, activeRequest)
				if compactErr != nil {
					return RunResult{Messages: sessionMessages, Usage: usage}, compactErr
				}
				sessionMessages = compacted
				reactiveRetries++
				continue
			}
			return RunResult{Messages: sessionMessages, Usage: usage}, err
		}
		reactiveRetries = 0

		assistantMessage := turn.Assistant
		hasToolUse := turn.HasTools
		if turn.Truncated() {
			if !hasEscalated {
				maxTokens = escalatedMaxTokens
				hasEscalated = true
				logger.Warn("[LLM] response truncated, retrying with max_tokens=%d", maxTokens)
				continue
			}

			// 不完整的 tool_use 尚未执行，不能加入历史后直接续写，否则会留下缺失 tool_result 的调用。
			if turn.HasTools {
				return RunResult{Messages: sessionMessages, Usage: usage}, ErrResponseTruncated
			}
			// 纯文本才允许从断点续写；这不会重放已经执行过的工具。

			sessionMessages = append(sessionMessages, assistantMessage)
			if recoveryCount < maxRecoveryRetries {
				sessionMessages = append(sessionMessages, protocol.Message{Role: protocol.RoleUser, Content: continuationPrompt})
				recoveryCount++
				logger.Warn("[LLM] continuing truncated response, recovery %d/%d", recoveryCount, maxRecoveryRetries)
				continue
			}
			return RunResult{Messages: sessionMessages, Output: protocol.Text(assistantMessage), Usage: usage}, nil
		}
		maxTokens = DefaultMaxTokens
		hasEscalated = false

		sessionMessages = append(sessionMessages, assistantMessage)

		// 兼容供应商的 stop_reason 可能不准确，因此只根据真实 tool_use block 判断是否执行工具。
		// 没有真实工具块时不能追加空的 user/tool_result 回合。
		if !hasToolUse {
			decision, err := r.hooks.TriggerStop(ctx, hooks.StopContext{
				ToolCallCnt: toolCallCnt,
				TurnCount:   turnCount,
				TotalTokens: tokenBaseline + usage.Total(),
				Messages:    protocol.CloneMessages(sessionMessages),
			})
			if err != nil {
				return RunResult{Messages: sessionMessages, Usage: usage}, err
			}
			if decision.Action == hooks.StopBlock {
				feedback := strings.TrimSpace(decision.Reason)
				if feedback == "" {
					feedback = "[Stop hook blocked completion. Continue working.]"
				}
				sessionMessages = append(sessionMessages, protocol.Message{
					Role:    protocol.RoleUser,
					Content: feedback,
				})
				continue
			}

			// s09：Stop gate 允许返回后再提取长期知识；错误只记录，不改变本轮工作结果。
			extractionMessages := append(protocol.CloneMessages(extractionSource), assistantMessage)
			report := r.context.Finalize(ctx, extractionMessages)
			if report.ExtractError != nil {
				logger.Error("[Memory] extraction skipped: %v", report.ExtractError)
			} else if report.Extracted > 0 {
				logger.Info("[Memory] extracted %d new memories", report.Extracted)
				if report.ConsolidateErr != nil {
					logger.Error("[Memory] consolidation skipped: %v", report.ConsolidateErr)
				} else if report.Before != report.After {
					logger.Info("[Memory] consolidated %d -> %d memories", report.Before, report.After)
				}
			}

			finalText := strings.TrimSpace(protocol.Text(assistantMessage))
			if finalText != "" {
				return RunResult{
					Messages: sessionMessages,
					Output:   finalText,
					Usage:    usage,
					Stop:     decision,
				}, nil
			}
			return RunResult{Messages: sessionMessages, Usage: usage, Stop: decision}, nil
		}

		roundsSinceTodo++
		toolResults := turn.Tools.Results
		toolCallCnt += turn.Tools.Count
		if turn.Tools.Stop {
			sessionMessages = turn.Tools.Messages
			sessionMessages = append(sessionMessages, protocol.Message{
				Role:    protocol.RoleUser,
				Content: compactToolResultText(toolResults),
			})
			continue
		}
		if len(toolResults) == 0 {
			return RunResult{Messages: sessionMessages, Usage: usage}, nil
		}
		if messageUsesTool(assistantMessage, "todo_write") {
			roundsSinceTodo = 0
		} else if roundsSinceTodo >= 3 {
			toolResults = append(toolResults, protocol.ContentBlock{
				Type: protocol.BlockText,
				Text: prompt.TodoReminder(),
			})
			roundsSinceTodo = 0
		}

		sessionMessages = append(sessionMessages, protocol.Message{
			Role:   protocol.RoleUser,
			Blocks: toolResults,
		})
	}
}
