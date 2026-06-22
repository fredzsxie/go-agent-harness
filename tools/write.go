package tools

import (
	"context"
	"os"
	"path/filepath"
)

func RunWriteFile(ctx context.Context, input any) (string, error) {
	payload := input.(map[string]any)
	path, _ := payload["path"].(string)
	content, _ := payload["content"].(string)
	if path == "" {
		return "", os.ErrInvalid
	}

	fullPath, err := SafePath(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		return "", err
	}
	return "", nil
}
