// Package loop 中的 runner 实现主代理循环，
// 负责驱动 LLM、工具调用、todo reminder 和消息历史推进。
package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"go-agent-harness/config"
	"go-agent-harness/internal/compact"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/prompt"
)

type Runner struct {
	client          anthropic.Client
	model           string
	registry        *Registry
	hooks           *hooks.Manager
	compact         *compact.Manager
	systemPrompt    string
	roundsSinceTodo int
}

func NewRunner(cfg config.LLMConfig, registry *Registry, hookManager *hooks.Manager, systemPrompt string) *Runner {
	if hookManager == nil {
		hookManager = hooks.NewManager()
	}
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = prompt.Main("")
	}
	return &Runner{
		client:       NewAnthropicClient(cfg),
		model:        cfg.Model,
		registry:     registry,
		hooks:        hookManager,
		compact:      compact.New(compact.Config{}),
		systemPrompt: systemPrompt,
	}
}

func (r *Runner) Run(ctx context.Context, messages []Message) (RunResult, error) {
	sessionMessages := CloneMessages(messages)

	if userPrompt := LatestUserPrompt(sessionMessages); userPrompt != "" {
		r.hooks.TriggerUserPromptSubmit(userPrompt)
	}
	if r.shouldInjectTodoReminder(sessionMessages) {
		sessionMessages = append(sessionMessages, Message{
			Role:    RoleUser,
			Content: prompt.TodoReminder(),
		})
		r.roundsSinceTodo = 0
	}

	toolCallCnt := 0
	reactiveRetries := 0
	for {
		// always compact context before LLM calling
		prepared, _, err := r.compact.Prepare(ctx, sessionMessages, r.summarizeCompactHistory)
		if err != nil {
			return RunResult{}, err
		}
		sessionMessages = prepared

		resp, err := r.client.Messages.New(ctx, anthropic.MessageNewParams{
			MaxTokens: 8000,
			Model:     anthropic.Model(r.model),
			Messages:  ToAnthropicMessages(sessionMessages),
			Tools:     ToolParams(r.registry),
			System:    []anthropic.TextBlockParam{{Text: r.systemPrompt}},
		})
		if err != nil {
			if isPromptTooLong(err) && reactiveRetries < r.compact.MaxReactiveRetries() {
				compacted, compactErr := r.compact.ReactiveCompact(ctx, sessionMessages, r.summarizeCompactHistory)
				if compactErr != nil {
					return RunResult{}, compactErr
				}
				sessionMessages = compacted
				reactiveRetries++
				continue
			}
			return RunResult{}, err
		}
		reactiveRetries = 0

		assistantMessage := ParseAnthropicAssistantMessage(resp.Content)
		sessionMessages = append(sessionMessages, assistantMessage)

		// stop_reason != tool_use ==> 本轮对话结束
		if resp.StopReason != anthropic.StopReasonToolUse {
			if force := r.hooks.TriggerStop(hooks.StopContext{ToolCallCnt: toolCallCnt}); force != "" {
				sessionMessages = append(sessionMessages, Message{
					Role:    RoleUser,
					Content: force,
				})
				continue
			}

			finalText := strings.TrimSpace(assistantMessage.Content)
			if finalText != "" {
				return RunResult{
					Messages: sessionMessages,
					Output:   finalText,
				}, nil
			}
			if len(resp.Content) == 0 {
				return RunResult{}, fmt.Errorf("empty response from model")
			}
			return RunResult{Messages: sessionMessages}, nil
		}

		r.roundsSinceTodo++
		toolResults, used, compactedMessages, compacted, err := r.executeToolUses(ctx, sessionMessages, assistantMessage.Blocks)
		if err != nil {
			return RunResult{}, err
		}
		toolCallCnt += used
		if compacted {
			sessionMessages = compactedMessages
			sessionMessages = append(sessionMessages, Message{
				Role:    RoleUser,
				Content: compactToolResultText(toolResults),
			})
			continue
		}
		if len(toolResults) == 0 {
			return RunResult{}, fmt.Errorf("model requested tool_use without tool blocks")
		}

		sessionMessages = append(sessionMessages, Message{
			Role:   RoleUser,
			Blocks: toolResults,
		})
	}
}

func compactToolResultText(results []ContentBlock) string {
	if len(results) == 0 || strings.TrimSpace(results[0].Text) == "" {
		return "[Compacted. Conversation history has been summarized.]"
	}
	return results[0].Text
}

func (r *Runner) executeToolUses(ctx context.Context, sessionMessages []Message, blocks []ContentBlock) ([]ContentBlock, int, []Message, bool, error) {
	results := make([]ContentBlock, 0, len(blocks))
	toolCallCnt := 0

	for _, block := range blocks {
		if block.Type != BlockToolUse {
			continue
		}
		toolCallCnt++

		if block.ToolName == "compact" {
			compacted, err := r.compact.CompactHistory(ctx, sessionMessages, r.summarizeCompactHistory)
			if err != nil {
				return nil, toolCallCnt, nil, false, err
			}
			results = append(results, ContentBlock{
				Type:      BlockToolResult,
				ToolUseID: block.ToolUseID,
				Text:      "[Compacted. Conversation history has been summarized.]",
			})
			return results, toolCallCnt, compacted, true, nil
		}

		call := hooks.ToolCall{
			ID:    block.ToolUseID,
			Name:  block.ToolName,
			Input: block.Input,
		}

		result := ""
		isError := false
		if blocked := r.hooks.TriggerPreToolUse(call); blocked != "" {
			result = blocked
			isError = true
		} else {
			output, err := r.registry.Dispatch(ctx, call.Name, call.Input)
			if err != nil {
				output = err.Error()
				isError = true
			}
			result = output
			r.hooks.TriggerPostToolUse(call, result)
			if call.Name == "todo_write" {
				r.roundsSinceTodo = 0
			}
		}

		results = append(results, ContentBlock{
			Type:      BlockToolResult,
			ToolUseID: call.ID,
			Text:      result,
			IsError:   isError,
		})
	}

	return results, toolCallCnt, nil, false, nil
}

func (r *Runner) shouldInjectTodoReminder(messages []Message) bool {
	return r.roundsSinceTodo >= 3 && len(messages) > 0
}

// Call LLM API to compact history conversation
func (r *Runner) summarizeCompactHistory(ctx context.Context, messages []compact.Message) (string, error) {
	raw, err := compactPromptPayload(messages)
	if err != nil {
		return "", err
	}
	if len(raw) > 80000 {
		raw = raw[:80000]
	}

	promptText := "Summarize this coding-agent conversation so work can continue.\n" +
		"Preserve: 1. current goal, 2. key findings/decisions, 3. files read/changed, " +
		"4. remaining work, 5. user constraints.\nBe compact but concrete.\n\n" + raw

	resp, err := r.client.Messages.New(ctx, anthropic.MessageNewParams{
		MaxTokens: 2000,
		Model:     anthropic.Model(r.model),
		Messages: []anthropic.MessageParam{{
			Role:    anthropic.MessageParamRoleUser,
			Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(promptText)},
		}},
	})
	if err != nil {
		return "", err
	}

	var parts []string
	for _, block := range resp.Content {
		if text := block.AsText(); strings.TrimSpace(text.Text) != "" {
			parts = append(parts, text.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n")), nil
}

func compactPromptPayload(messages []compact.Message) (string, error) {
	raw, err := json.Marshal(messages)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func isPromptTooLong(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "prompt_too_long") ||
		strings.Contains(text, "too many tokens") ||
		strings.Contains(text, "context length") ||
		strings.Contains(text, "context window")
}
