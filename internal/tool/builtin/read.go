package builtin

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

func RunReadFile(ctx context.Context, input any) (string, error) {
	return defaultTools.RunReadFile(ctx, input)
}

func (t *Tools) RunReadFile(_ context.Context, input any) (string, error) {
	path, _ := input.(map[string]any)["path"].(string)
	if path == "" {
		return "", os.ErrInvalid
	}

	fullPath, err := t.SafePath(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(data) {
		return "", fmt.Errorf("file is not valid UTF-8: %s", path)
	}

	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	limit := intValue(input.(map[string]any)["limit"])
	if limit > 0 && len(lines) > limit {
		remaining := len(lines) - limit
		lines = append(lines[:limit], fmt.Sprintf("... (%d more lines)", remaining))
	}
	return strings.Join(lines, "\n"), nil
}

func intValue(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case int32:
		return int(number)
	case int64:
		return int(number)
	case float64:
		return int(number)
	default:
		return 0
	}
}
