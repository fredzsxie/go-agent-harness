package model

import (
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"

	"go-agent-harness/internal/agent"
)

func toAnthropicTools(specs []agent.ToolSpec) []anthropic.ToolUnionParam {
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

func toAnthropicMessages(messages []agent.Message) []anthropic.MessageParam {
	params := make([]anthropic.MessageParam, 0, len(messages))
	for _, message := range messages {
		blocks := toAnthropicBlocks(message)
		if len(blocks) == 0 {
			continue
		}
		switch message.Role {
		case agent.RoleUser:
			params = append(params, anthropic.MessageParam{Role: anthropic.MessageParamRoleUser, Content: blocks})
		case agent.RoleAssistant:
			params = append(params, anthropic.MessageParam{Role: anthropic.MessageParamRoleAssistant, Content: blocks})
		}
	}
	return params
}

func toAnthropicBlocks(message agent.Message) []anthropic.ContentBlockParamUnion {
	if len(message.Blocks) == 0 {
		if strings.TrimSpace(message.Content) == "" {
			return nil
		}
		return []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(message.Content)}
	}

	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(message.Blocks))
	for _, block := range message.Blocks {
		switch block.Type {
		case agent.BlockText:
			blocks = append(blocks, anthropic.NewTextBlock(block.Text))
		case agent.BlockToolUse:
			blocks = append(blocks, anthropic.NewToolUseBlock(block.ToolUseID, block.Input, block.ToolName))
		case agent.BlockToolResult:
			blocks = append(blocks, anthropic.NewToolResultBlock(block.ToolUseID, block.Text, block.IsError))
		}
	}
	return blocks
}

func parseAssistantMessage(content []anthropic.ContentBlockUnion) agent.Message {
	message := agent.Message{Role: agent.RoleAssistant, Blocks: make([]agent.ContentBlock, 0, len(content))}
	for _, block := range content {
		if text := block.AsText(); text.Text != "" {
			message.Blocks = append(message.Blocks, agent.ContentBlock{Type: agent.BlockText, Text: text.Text})
			if message.Content != "" {
				message.Content += "\n"
			}
			message.Content += text.Text
			continue
		}

		if toolUse := block.AsToolUse(); toolUse.Name != "" {
			message.Blocks = append(message.Blocks, agent.ContentBlock{
				Type:      agent.BlockToolUse,
				ToolUseID: toolUse.ID,
				ToolName:  toolUse.Name,
				Input:     cloneMap(normalizeToolInput(toolUse.Input)),
			})
		}
	}
	return message
}

func normalizeToolInput(input any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	if value, ok := input.(map[string]any); ok {
		return value
	}
	if raw, err := json.Marshal(input); err == nil {
		var parsed map[string]any
		if json.Unmarshal(raw, &parsed) == nil {
			return parsed
		}
	}
	return map[string]any{"command": fmt.Sprint(input)}
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
