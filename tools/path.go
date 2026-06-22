package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var workspaceRoot = mustWorkspaceRoot()

func mustWorkspaceRoot() string {
	root, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return root
}

func SafePath(p string) (string, error) {
	cleaned := filepath.Clean(p)
	full := filepath.Join(workspaceRoot, cleaned)
	resolved, err := filepath.EvalSymlinks(full)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		resolved = full
	}
	rootPrefix := workspaceRoot + string(os.PathSeparator)
	if resolved != workspaceRoot && !strings.HasPrefix(resolved, rootPrefix) {
		return "", fmt.Errorf("path escapes workspace: %s", p)
	}
	return resolved, nil
}
