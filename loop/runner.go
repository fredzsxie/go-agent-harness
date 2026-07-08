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

func (r *Runner) Run(ctx context.Context, messages []Message) (RunResult, error) {
	sessionMessages := cloneMessages(messages)

	if prompt := latestUserPrompt(sessionMessages); prompt != "" {
		r.hooks.TriggerUserPromptSubmit(prompt)
	}
	if r.shouldInjectTodoReminder(sessionMessages) {
		sessionMessages = append(sessionMessages, Message{
			Role:    RoleUser,
			Content: todoReminder,
		})
		r.roundsSinceTodo = 0
	}

	toolCallCnt := 0
	for {
		params := anthropic.MessageNewParams{
			MaxTokens: 8000,
			Model:     anthropic.Model(r.model),
			Messages:  toAnthropicMessages(sessionMessages),
			Tools:     r.toolParams(),
			System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		}

		resp, err := r.client.Messages.New(ctx, params)
		if err != nil {
			return RunResult{}, err
		}

		assistantMessage, finalText := parseAssistantResponse(resp.Content)
		sessionMessages = append(sessionMessages, assistantMessage)

		// 退出循环的信号（stop_reason != "tool_use"）
		if resp.StopReason != anthropic.StopReasonToolUse {
			if force := r.hooks.TriggerStop(hooks.StopContext{ToolCallCnt: toolCallCnt}); force != "" {
				sessionMessages = append(sessionMessages, Message{
					Role:    RoleUser,
					Content: force,
				})
				continue
			}
			if finalText != "" {
				return RunResult{
					Messages: sessionMessages,
					Output:   strings.TrimSpace(finalText),
				}, nil
			}
			if len(resp.Content) == 0 {
				return RunResult{}, fmt.Errorf("empty response from model")
			}
			return RunResult{Messages: sessionMessages}, nil
		}

		r.roundsSinceTodo++
		toolResults := make([]ContentBlock, 0, len(assistantMessage.Blocks))
		for _, block := range assistantMessage.Blocks {
			// fmt.Printf("[Block] block detail: %+v\n", block)
			if block.Type != BlockToolUse {
				continue
			}

			call := hooks.ToolCall{
				ID:    block.ToolUseID,
				Name:  block.ToolName,
				Input: block.Input,
			}
			toolCallCnt++
			result := ""
			isError := false

			if blocked := r.hooks.TriggerPreToolUse(call); blocked != "" {
				result = blocked
				isError = true
			} else {
				// 执行工具调用
				output, err := r.registry.Dispatch(ctx, call.Name, call.Input)
				if err != nil {
					output = err.Error()
					isError = true
				}
				result = output

				// 重置todo_write计数
				if call.Name == "todo_write" {
					r.roundsSinceTodo = 0
				}

				r.hooks.TriggerPostToolUse(call, result)
			}

			toolResults = append(toolResults, ContentBlock{
				Type:      BlockToolResult,
				ToolUseID: call.ID,
				Text:      result,
				IsError:   isError,
			})
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

func parseAssistantResponse(content []anthropic.ContentBlockUnion) (Message, string) {
	message := Message{
		Role:   RoleAssistant,
		Blocks: make([]ContentBlock, 0, len(content)),
	}
	finalText := ""

	for _, block := range content {
		if text := block.AsText(); text.Text != "" {
			message.Blocks = append(message.Blocks, ContentBlock{
				Type: BlockText,
				Text: text.Text,
			})
			if message.Content != "" {
				message.Content += "\n"
			}
			message.Content += text.Text
			if finalText != "" {
				finalText += "\n"
			}
			finalText += text.Text
			continue
		}

		if toolUse := block.AsToolUse(); toolUse.Name != "" {
			message.Blocks = append(message.Blocks, ContentBlock{
				Type:      BlockToolUse,
				ToolUseID: toolUse.ID,
				ToolName:  toolUse.Name,
				Input:     parseToolInput(toolUse.Input),
			})
		}
	}

	return message, finalText
}

func toAnthropicMessages(messages []Message) []anthropic.MessageParam {
	anthropicMessages := make([]anthropic.MessageParam, 0, len(messages))
	for _, message := range messages {
		blocks := toAnthropicBlocks(message)
		if len(blocks) == 0 {
			continue
		}
		switch message.Role {
		case RoleUser:
			anthropicMessages = append(anthropicMessages, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: blocks,
			})
		case RoleAssistant:
			anthropicMessages = append(anthropicMessages, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleAssistant,
				Content: blocks,
			})
		}
	}
	return anthropicMessages
}

func toAnthropicBlocks(message Message) []anthropic.ContentBlockParamUnion {
	if len(message.Blocks) > 0 {
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(message.Blocks))
		for _, block := range message.Blocks {
			switch block.Type {
			case BlockText:
				blocks = append(blocks, anthropic.NewTextBlock(block.Text))
			case BlockToolUse:
				blocks = append(blocks, anthropic.NewToolUseBlock(block.ToolUseID, block.Input, block.ToolName))
			case BlockToolResult:
				blocks = append(blocks, anthropic.NewToolResultBlock(block.ToolUseID, block.Text, block.IsError))
			}
		}
		return blocks
	}

	if strings.TrimSpace(message.Content) == "" {
		return nil
	}
	return []anthropic.ContentBlockParamUnion{
		anthropic.NewTextBlock(message.Content),
	}
}

func cloneMessages(messages []Message) []Message {
	cloned := make([]Message, 0, len(messages))
	for _, message := range messages {
		copyMessage := Message{
			Role:    message.Role,
			Content: message.Content,
		}
		if len(message.Blocks) > 0 {
			copyMessage.Blocks = make([]ContentBlock, 0, len(message.Blocks))
			for _, block := range message.Blocks {
				copyBlock := block
				if block.Input != nil {
					copyBlock.Input = cloneMap(block.Input)
				}
				copyMessage.Blocks = append(copyMessage.Blocks, copyBlock)
			}
		}
		cloned = append(cloned, copyMessage)
	}
	return cloned
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func latestUserPrompt(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != RoleUser {
			continue
		}
		if strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
		for _, block := range messages[i].Blocks {
			if block.Type == BlockText && strings.TrimSpace(block.Text) != "" {
				return block.Text
			}
		}
	}
	return ""
}

func (r *Runner) shouldInjectTodoReminder(messages []Message) bool {
	return r.roundsSinceTodo >= 3 && len(messages) > 0
}
