package loop

import (
	"context"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"go-agent-harness/config"
	"go-agent-harness/internal/hooks"
)

const systemPrompt = `You are Claude Code, Anthropic's official CLI for Claude.
You are a helpful coding assistant.
Before starting any multi-step task, use todo_write to plan your steps and keep statuses updated.
Only use emojis if the user explicitly requests it.
`

const todoReminder = "<reminder>Update your todos.</reminder>"

type Runner struct {
	client          anthropic.Client
	model           string
	registry        *Registry
	hooks           *hooks.Manager
	roundsSinceTodo int
}

func NewRunner(cfg config.LLMConfig, registry *Registry, hookManager *hooks.Manager) *Runner {
	if hookManager == nil {
		hookManager = hooks.NewManager()
	}
	return &Runner{
		client: anthropic.NewClient(
			option.WithBaseURL(cfg.BaseURL),
			option.WithAPIKey(cfg.APIKey),
		),
		model:    cfg.Model,
		registry: registry,
		hooks:    hookManager,
	}
}

func (r *Runner) Run(ctx context.Context, messages []Message) (string, error) {
	// Runner 负责 hook 执行，App 只负责装配和传入消息
	if prompt := latestUserPrompt(messages); prompt != "" {
		r.hooks.TriggerUserPromptSubmit(prompt)
	}

	// 先把本轮的历史消息转换成 Anthropic Messages API 需要的格式。
	anthropicMessages := make([]anthropic.MessageParam, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case RoleUser:
			anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(anthropic.NewTextBlock(message.Content)))
		case RoleAssistant:
			anthropicMessages = append(anthropicMessages, anthropic.NewAssistantMessage(anthropic.NewTextBlock(message.Content)))
		}
	}

	if r.shouldInjectTodoReminder(messages) {
		anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(anthropic.NewTextBlock(todoReminder)))
		r.roundsSinceTodo = 0
	}

	// 限制最大迭代次数，避免工具调用链意外死循环。
	toolCallCnt := 0
	for i := 0; i < 8; i++ {
		params := anthropic.MessageNewParams{
			MaxTokens: 8000,
			Model:     anthropic.Model(r.model),
			Messages:  anthropicMessages,
			Tools:     r.toolParams(),
			System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		}

		resp, err := r.client.Messages.New(ctx, params)
		if err != nil {
			return "", err
		}

		toolUsed := false
		finalText := ""
		for _, block := range resp.Content {
			if toolUse := block.AsToolUse(); toolUse.Name != "" {
				toolUsed = true
				r.roundsSinceTodo++
				call := hooks.ToolCall{
					ID:    toolUse.ID,
					Name:  toolUse.Name,
					Input: parseToolInput(toolUse.Input),
				}
				toolCallCnt++
				result := ""

				if blocked := r.hooks.TriggerPreToolUse(call); blocked != "" {
					result = blocked
				} else {
					output, err := r.registry.Dispatch(ctx, call.Name, call.Input)
					if err != nil {
						output = err.Error()
					}
					result = output
					if call.Name == "todo_write" {
						r.roundsSinceTodo = 0
					}
					r.hooks.TriggerPostToolUse(call, result)
				}

				anthropicMessages = append(anthropicMessages,
					anthropic.NewAssistantMessage(anthropic.NewToolUseBlock(toolUse.ID, toolUse.Input, toolUse.Name)),
					anthropic.MessageParam{Role: anthropic.MessageParamRoleUser, Content: []anthropic.ContentBlockParamUnion{anthropic.NewToolResultBlock(toolUse.ID, result, false)}},
				)
				break
			}
			if text := block.AsText(); text.Text != "" {
				if finalText != "" {
					finalText += "\n"
				}
				finalText += text.Text
			}
		}

		// 继续循环的信号（stop_reason == 'tool_use'）
		if toolUsed || resp.StopReason == anthropic.StopReasonToolUse {
			continue
		}

		// 退出循环的信号（stop_reason != "tool_use"）
		if force := r.hooks.TriggerStop(hooks.StopContext{ToolCallCnt: toolCallCnt}); force != "" {
			anthropicMessages = append(anthropicMessages, anthropic.NewUserMessage(anthropic.NewTextBlock(force)))
			continue
		}

		if finalText != "" {
			return strings.TrimSpace(finalText), nil
		}
		if len(resp.Content) == 0 {
			return "", fmt.Errorf("empty response from model")
		}
		return "", nil
	}

	return "", fmt.Errorf("agent loop exceeded max iterations")
}

func (r *Runner) toolParams() []anthropic.ToolUnionParam {
	specs := r.registry.Specs()
	params := make([]anthropic.ToolUnionParam, 0, len(specs))
	for _, spec := range specs {
		params = append(params, anthropic.ToolUnionParam{OfTool: &anthropic.ToolParam{
			Name:        spec.Name,
			Description: anthropic.String(spec.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: spec.Properties,
				Required:   spec.Required,
			},
		}})
	}
	return params
}

func latestUserPrompt(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleUser {
			return messages[i].Content
		}
	}
	return ""
}

// 当连续 3 轮没有更新 todo 时，会在下一次模型调用前注入 <todoReminder>，并在调用 todo_write 后重置计数
func (r *Runner) shouldInjectTodoReminder(messages []Message) bool {
	return r.roundsSinceTodo >= 3 && len(messages) > 0
}
