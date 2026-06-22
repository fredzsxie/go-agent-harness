package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"go-agent-harness/config"
)

const systemPrompt = `You are Claude Code, Anthropic's official CLI for Claude.
You are a helpful coding assistant.
You help users with software engineering tasks.
Prefer editing existing files to creating new ones.
Do not add features, refactor, or introduce abstractions beyond what the task requires.
Only use emojis if the user explicitly requests it.
`

type Runner struct {
	client   anthropic.Client
	model    string
	registry *Registry
}

func NewRunner(cfg config.LLMConfig, registry *Registry) *Runner {
	return &Runner{
		client: anthropic.NewClient(
			option.WithBaseURL(cfg.BaseURL),
			option.WithAPIKey(cfg.APIKey),
		),
		model:    cfg.Model,
		registry: registry,
	}
}

func (r *Runner) Run(ctx context.Context, messages []Message) (string, error) {
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

	// 限制最大迭代次数，避免工具调用链意外死循环。
	for i := 0; i < 8; i++ {
		params := anthropic.MessageNewParams{
			MaxTokens: 8000,
			Model:     anthropic.Model(r.model),
			Messages:  anthropicMessages,
			Tools: []anthropic.ToolUnionParam{
				{OfTool: &anthropic.ToolParam{Name: "bash", Description: anthropic.String("run a shell command"), InputSchema: anthropic.ToolInputSchemaParam{Required: []string{"command"}}}},
				{OfTool: &anthropic.ToolParam{Name: "read_file", Description: anthropic.String("read file contents"), InputSchema: anthropic.ToolInputSchemaParam{Required: []string{"path"}}}},
				{OfTool: &anthropic.ToolParam{Name: "write_file", Description: anthropic.String("write content to a file"), InputSchema: anthropic.ToolInputSchemaParam{Required: []string{"path", "content"}}}},
				{OfTool: &anthropic.ToolParam{Name: "edit_file", Description: anthropic.String("replace exact text in a file once"), InputSchema: anthropic.ToolInputSchemaParam{Required: []string{"path", "old_text", "new_text"}}}},
				{OfTool: &anthropic.ToolParam{Name: "glob", Description: anthropic.String("find files matching a glob pattern"), InputSchema: anthropic.ToolInputSchemaParam{Required: []string{"pattern"}}}},
			},
			System: []anthropic.TextBlockParam{{Text: systemPrompt}},
		}

		resp, err := r.client.Messages.New(ctx, params)
		if err != nil {
			return "", err
		}

		toolUsed := false
		finalText := ""
		for _, block := range resp.Content {
			if toolUse := block.AsToolUse(); toolUse.Name != "" {
				// 执行工具调用，收集结果
				toolUsed = true
				fmt.Printf("[tool] call: %s\n", toolUse.Name)
				fmt.Printf("[tool] input: %s\n", prettyPrintValue(toolUse.Input))
				result, err := r.registry.Dispatch(ctx, toolUse.Name, parseToolInput(toolUse.Input))
				if err != nil {
					result = err.Error()
				}
				fmt.Printf("[tool] result: %s\n\n", result)

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

func prettyPrintValue(v any) string {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err == nil {
		return string(raw)
	}
	return fmt.Sprint(v)
}
