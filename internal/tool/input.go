package tool

import (
	"encoding/json"
	"fmt"
)

// normalizeInput 兼容结构体和早期 Bash 字符串参数；各工具仍需验证自己的输入。
func normalizeInput(input any) map[string]any {
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
