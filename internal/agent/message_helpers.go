// message 辅助函数负责消息克隆、tool input 归一化
// 以及最近一次 user / assistant 文本提取，供主循环和子能力复用。
package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"go-agent-harness/internal/protocol"
)

func CloneMessages(messages []protocol.Message) []protocol.Message {
	cloned := make([]protocol.Message, 0, len(messages))
	for _, message := range messages {
		copyMessage := protocol.Message{
			Role:    message.Role,
			Content: message.Content,
		}
		if len(message.Blocks) > 0 {
			copyMessage.Blocks = make([]protocol.ContentBlock, 0, len(message.Blocks))
			for _, block := range message.Blocks {
				copyBlock := block
				if block.Input != nil {
					copyBlock.Input = CloneMap(block.Input)
				}
				copyMessage.Blocks = append(copyMessage.Blocks, copyBlock)
			}
		}
		cloned = append(cloned, copyMessage)
	}
	return cloned
}

func CloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func NormalizeToolInput(input any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	if m, ok := input.(map[string]any); ok {
		return m
	}
	if raw, err := json.Marshal(input); err == nil {
		var parsed map[string]any
		if json.Unmarshal(raw, &parsed) == nil {
			return parsed
		}
	}
	return map[string]any{"command": fmt.Sprint(input)}
}

func LatestUserPrompt(messages []protocol.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != protocol.RoleUser {
			continue
		}
		if strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
		for _, block := range messages[i].Blocks {
			if block.Type == protocol.BlockText && strings.TrimSpace(block.Text) != "" {
				return block.Text
			}
		}
	}
	return ""
}

func LatestAssistantText(messages []protocol.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != protocol.RoleAssistant {
			continue
		}
		if strings.TrimSpace(messages[i].Content) != "" {
			return messages[i].Content
		}
	}
	return ""
}

// ActiveRequest 收集上一次 assistant 回复后新增的用户请求，自动唤醒时则沿用最近的真实请求。
func ActiveRequest(messages []protocol.Message) string {
	lastAssistant := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == protocol.RoleAssistant {
			lastAssistant = i
			break
		}
	}
	requests := make([]string, 0, len(messages)-lastAssistant-1)
	for _, message := range messages[lastAssistant+1:] {
		if message.Role != protocol.RoleUser {
			continue
		}
		if text := userMessageText(message); text != "" {
			requests = append(requests, text)
		}
	}
	if len(requests) > 0 {
		return strings.Join(requests, "\n")
	}
	return LatestUserPrompt(messages)
}

func userMessageText(message protocol.Message) string {
	parts := make([]string, 0, len(message.Blocks)+1)
	if text := strings.TrimSpace(message.Content); text != "" {
		parts = append(parts, text)
	}
	for _, block := range message.Blocks {
		if block.Type == protocol.BlockText && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, strings.TrimSpace(block.Text))
		}
	}
	return strings.Join(parts, "\n")
}
