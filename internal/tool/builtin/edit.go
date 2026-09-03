package builtin

import (
	"context"
	"os"
	"strings"
)

func RunEditFile(ctx context.Context, input any) (string, error) {
	return defaultTools.RunEditFile(ctx, input)
}

func (t *Tools) RunEditFile(_ context.Context, input any) (string, error) {
	payload := input.(map[string]any)
	path, _ := payload["path"].(string)
	oldText, _ := payload["old_text"].(string)
	newText, _ := payload["new_text"].(string)
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
	text := string(data)
	if !strings.Contains(text, oldText) {
		return "", os.ErrNotExist
	}
	updated := strings.Replace(text, oldText, newText, 1)
	if err := os.WriteFile(fullPath, []byte(updated), 0o644); err != nil {
		return "", err
	}
	return "", nil
}
