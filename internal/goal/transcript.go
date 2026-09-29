package goal

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"go-agent-harness/internal/protocol"
)

const omittedMarker = "\n...[middle omitted]...\n"

// Transcript 保留最近的完整消息；只有最新消息本身超限时才裁掉其中间部分。
func Transcript(messages []protocol.Message, maxCharacters int) string {
	if maxCharacters <= 0 {
		maxCharacters = DefaultTranscriptCharacters
	}
	rendered := make([]string, len(messages))
	for index, message := range messages {
		rendered[index] = strings.ToUpper(string(message.Role)) + ":\n" + renderContent(message)
	}

	selected := make([]string, 0, len(rendered))
	size := 0
	for index := len(rendered) - 1; index >= 0; index-- {
		item := rendered[index]
		itemSize := utf8.RuneCountInString(item)
		if len(selected) == 0 && itemSize > maxCharacters {
			selected = append(selected, trimMiddle(item, maxCharacters))
			break
		}
		separator := 0
		if len(selected) > 0 {
			separator = 2
		}
		if size+separator+itemSize > maxCharacters {
			break
		}
		selected = append(selected, item)
		size += separator + itemSize
	}

	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return strings.Join(selected, "\n\n")
}

func renderContent(message protocol.Message) string {
	if len(message.Blocks) == 0 {
		return message.Content
	}
	parts := make([]string, 0, len(message.Blocks))
	for _, block := range message.Blocks {
		switch block.Type {
		case protocol.BlockText:
			parts = append(parts, block.Text)
		case protocol.BlockToolUse:
			input, _ := json.Marshal(block.Input)
			parts = append(parts, fmt.Sprintf("[tool_use %s %s]", block.ToolName, input))
		case protocol.BlockToolResult:
			label := "tool_result"
			if block.IsError {
				label += " error"
			}
			parts = append(parts, fmt.Sprintf("[%s %s]", label, block.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func trimMiddle(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	marker := []rune(omittedMarker)
	if limit <= len(marker) {
		return string(marker[:limit])
	}
	available := limit - len(marker)
	head := available * 3 / 4
	tail := available - head
	return string(runes[:head]) + omittedMarker + string(runes[len(runes)-tail:])
}
