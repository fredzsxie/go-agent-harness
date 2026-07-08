// Package loop 中的 runner 实现主代理循环，
// 负责驱动 LLM、工具调用、todo reminder 和消息历史推进。
package loop

import (
	"context"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"go-agent-harness/config"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/prompt"
)

type Runner struct {
	client          anthropic.Client
	model           string
	registry        *Registry
	hooks           *hooks.Manager
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
	for {
		resp, err := r.client.Messages.New(ctx, anthropic.MessageNewParams{
			MaxTokens: 8000,
			Model:     anthropic.Model(r.model),
			Messages:  ToAnthropicMessages(sessionMessages),
			Tools:     ToolParams(r.registry),
			System:    []anthropic.TextBlockParam{{Text: r.systemPrompt}},
		})
		if err != nil {
			return RunResult{}, err
		}

		assistantMessage := ParseAnthropicAssistantMessage(resp.Content)
		sessionMessages = append(sessionMessages, assistantMessage)

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
		toolResults, used := ExecuteToolUses(ctx, assistantMessage.Blocks, r.registry, r.hooks, func(call hooks.ToolCall, _ string, _ bool) {
			if call.Name == "todo_write" {
				r.roundsSinceTodo = 0
			}
		})
		toolCallCnt += used
		if len(toolResults) == 0 {
			return RunResult{}, fmt.Errorf("model requested tool_use without tool blocks")
		}

		sessionMessages = append(sessionMessages, Message{
			Role:   RoleUser,
			Blocks: toolResults,
		})
	}
}

func (r *Runner) shouldInjectTodoReminder(messages []Message) bool {
	return r.roundsSinceTodo >= 3 && len(messages) > 0
}
