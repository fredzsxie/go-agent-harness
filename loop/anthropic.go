// Package loop 中的 anthropic 适配层负责 Anthropic SDK 与本地消息结构、
// 工具 schema 之间的双向转换，避免转换细节散落在 runner 中。
package loop

import (
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"go-agent-harness/config"
)

func NewAnthropicClient(cfg config.LLMConfig) anthropic.Client {
	return anthropic.NewClient(
		option.WithBaseURL(cfg.BaseURL),
		option.WithAPIKey(cfg.APIKey),
	)
}

func ToolParams(registry *Registry) []anthropic.ToolUnionParam {
	specs := registry.Specs()
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

func ToAnthropicMessages(messages []Message) []anthropic.MessageParam {
	anthropicMessages := make([]anthropic.MessageParam, 0, len(messages))
	for _, message := range messages {
		blocks := ToAnthropicBlocks(message)
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

func ToAnthropicBlocks(message Message) []anthropic.ContentBlockParamUnion {
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
	return []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(message.Content)}
}

func ParseAnthropicAssistantMessage(content []anthropic.ContentBlockUnion) Message {
	message := Message{
		Role:   RoleAssistant,
		Blocks: make([]ContentBlock, 0, len(content)),
	}

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
			continue
		}

		if toolUse := block.AsToolUse(); toolUse.Name != "" {
			message.Blocks = append(message.Blocks, ContentBlock{
				Type:      BlockToolUse,
				ToolUseID: toolUse.ID,
				ToolName:  toolUse.Name,
				Input:     CloneMap(NormalizeToolInput(toolUse.Input)),
			})
		}
	}

	return message
}
