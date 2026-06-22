package tools

import (
	"context"
	"path/filepath"
	"strings"
)

func RunGlob(ctx context.Context, input any) (string, error) {
	pattern, _ := input.(map[string]any)["pattern"].(string)
	matches, err := filepath.Glob(filepath.Join(".", pattern))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", nil
	}
	return strings.Join(matches, "\n"), nil
}
