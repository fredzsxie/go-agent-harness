package builtin

import (
	"context"
	"os"
	"path/filepath"
)

func RunWriteFile(ctx context.Context, input any) (string, error) {
	return defaultTools.RunWriteFile(ctx, input)
}

func (t *Tools) RunWriteFile(_ context.Context, input any) (string, error) {
	payload := input.(map[string]any)
	path, _ := payload["path"].(string)
	content, _ := payload["content"].(string)
	if path == "" {
		return "", os.ErrInvalid
	}

	fullPath, err := t.SafePath(path)
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
