// Package workspace 提供统一的工作区根目录与路径解析能力，
// 让工具层和权限层共享同一套安全路径判断逻辑。
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Resolver struct {
	root string
}

var defaultResolver = mustCurrentResolver()

func mustCurrentResolver() *Resolver {
	cwd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	resolver, err := New(cwd)
	if err != nil {
		panic(err)
	}
	return resolver
}

func New(root string) (*Resolver, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("workspace root is required")
	}
	if !filepath.IsAbs(root) {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		root = absRoot
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		if os.IsNotExist(err) {
			resolvedRoot = filepath.Clean(root)
		} else {
			return nil, err
		}
	}
	return &Resolver{root: filepath.Clean(resolvedRoot)}, nil
}

func Root() string {
	return defaultResolver.Root()
}

// Default 返回主进程启动目录对应的 Resolver。
func Default() *Resolver {
	return defaultResolver
}

func Resolve(path string) (string, error) {
	return defaultResolver.Resolve(path)
}

func (r *Resolver) Root() string {
	return r.root
}

func (r *Resolver) Resolve(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}

	fullPath := path
	if !filepath.IsAbs(fullPath) {
		fullPath = filepath.Join(r.root, path)
	}
	fullPath = filepath.Clean(fullPath)

	resolved, err := resolvePath(fullPath)
	if err != nil {
		return "", err
	}
	if !containsPath(r.root, resolved) {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}
	return resolved, nil
}

func resolvePath(path string) (string, error) {
	attempt := path
	suffix := make([]string, 0, 4)

	for {
		resolved, err := filepath.EvalSymlinks(attempt)
		if err == nil {
			if len(suffix) == 0 {
				return filepath.Clean(resolved), nil
			}
			parts := append([]string{resolved}, suffix...)
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(attempt)
		if parent == attempt {
			return filepath.Clean(path), nil
		}
		suffix = append([]string{filepath.Base(attempt)}, suffix...)
		attempt = parent
	}
}

func containsPath(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(os.PathSeparator))
}
