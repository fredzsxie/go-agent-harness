// 消息辅助函数只处理协议数据，不依赖模型、工具执行器或业务状态。
package protocol

import (
	"strings"
)

// CloneMessages 隔离历史快照及其嵌套 JSON 参数；调用方可安全压缩或追加消息。
func CloneMessages(messages []Message) []Message {
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
					copyBlock.Input = CloneMap(block.Input)
				}
				copyMessage.Blocks = append(copyMessage.Blocks, copyBlock)
			}
		}
		cloned = append(cloned, copyMessage)
	}
	return cloned
}

// CloneMap 复制 JSON 对象及其子容器，保留原有标量类型。
func CloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func LatestUserPrompt(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != RoleUser {
			continue
		}
		if text := Text(messages[i]); strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func LatestAssistantText(messages []Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != RoleAssistant {
			continue
		}
		if text := Text(messages[i]); text != "" {
			return text
		}
	}
	return ""
}

// ActiveRequest 收集上一次 assistant 回复后新增的用户请求，自动唤醒时则沿用最近的真实请求。
func ActiveRequest(messages []Message) string {
	lastAssistant := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == RoleAssistant {
			lastAssistant = i
			break
		}
	}
	requests := make([]string, 0, len(messages)-lastAssistant-1)
	for _, message := range messages[lastAssistant+1:] {
		if message.Role != RoleUser {
			continue
		}
		if text := Text(message); text != "" {
			requests = append(requests, text)
		}
	}
	if len(requests) > 0 {
		return strings.Join(requests, "\n")
	}
	return LatestUserPrompt(messages)
}

// Text 提取普通文本；Blocks 非空时是唯一内容来源，避免 Content 的便捷副本被重复发送。
func Text(message Message) string {
	if len(message.Blocks) == 0 {
		return strings.TrimSpace(message.Content)
	}
	var parts []string
	for _, block := range message.Blocks {
		if block.Type == BlockText && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// cloneValue 复制工具参数中的 JSON 容器，避免嵌套对象跨消息快照共享可变状态。
func cloneValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return CloneMap(value)
	case []any:
		cloned := make([]any, len(value))
		for i, item := range value {
			cloned[i] = cloneValue(item)
		}
		return cloned
	case []string:
		return append([]string(nil), value...)
	default:
		return value
	}
}
