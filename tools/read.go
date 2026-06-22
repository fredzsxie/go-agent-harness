package tools

import (
	"context"
	"os"
)

func RunReadFile(ctx context.Context, input any) (string, error) {
	path, _ := input.(map[string]any)["path"].(string)
	if path == "" {
		return "", os.ErrInvalid
	}

	fullPath, err := SafePath(path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
