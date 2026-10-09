package memory

import (
	"fmt"
	"regexp"
	"strings"

	"go-agent-harness/internal/protocol"
)

func RecentUserText(messages []protocol.Message, maxItems int, maxChars int) string {
	if maxItems <= 0 {
		maxItems = 3
	}
	var parts []string
	for i := len(messages) - 1; i >= 0 && len(parts) < maxItems; i-- {
		if messages[i].Role != protocol.RoleUser {
			continue
		}
		text := messageText(messages[i])
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	reverse(parts)
	out := strings.Join(parts, "\n")
	if maxChars > 0 && len(out) > maxChars {
		out = out[:maxChars]
	}
	return out
}

func FormatRecentMessages(messages []protocol.Message, maxItems int, maxChars int) string {
	if maxItems <= 0 || maxItems > len(messages) {
		maxItems = len(messages)
	}
	start := len(messages) - maxItems
	if start < 0 {
		start = 0
	}

	parts := make([]string, 0, len(messages)-start)
	for _, message := range messages[start:] {
		text := messageText(message)
		if strings.TrimSpace(text) == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", message.Role, text))
	}
	out := strings.Join(parts, "\n")
	if maxChars > 0 && len(out) > maxChars {
		out = out[:maxChars]
	}
	return out
}

var slugPart = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = slugPart.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "memory"
	}
	return slug
}

func messageText(message protocol.Message) string {
	if strings.TrimSpace(message.Content) != "" {
		return message.Content
	}
	parts := make([]string, 0, len(message.Blocks))
	for _, block := range message.Blocks {
		switch block.Type {
		case protocol.BlockText, protocol.BlockToolResult:
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		case protocol.BlockToolUse:
			if block.ToolName != "" {
				parts = append(parts, "[tool_use "+block.ToolName+"]")
			}
		}
	}
	return strings.Join(parts, "\n")
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if len(line) > 80 {
				return line[:80]
			}
			return line
		}
	}
	return ""
}

func fallback(value, fallbackValue string) string {
	if strings.TrimSpace(value) == "" {
		return fallbackValue
	}
	return value
}

func reverse(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}
