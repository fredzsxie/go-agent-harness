package architecture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestInternalDependencyBoundaries 把关键分层约束变成回归测试。
// 只检查生产文件；测试可以通过 app 装配多个模块验证集成行为。
func TestInternalDependencyBoundaries(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate architecture test")
	}
	root := filepath.Dir(filepath.Dir(source))
	foundations := map[string]map[string]bool{
		"protocol": {},
		"tool":     {"protocol": true},
		"llm":      {"protocol": true, "tool": true},
	}
	independent := map[string]bool{"model": true, "mcp": true, "goal": true, "workflow": true, "memory": true, "compact": true}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		from := filepath.ToSlash(relative)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			const prefix = "go-agent-harness/internal/"
			if !strings.HasPrefix(imported, prefix) {
				continue
			}
			to := strings.TrimPrefix(imported, prefix)
			if to == "app" && from != "app" {
				t.Errorf("%s imports composition root app", path)
			}
			if allowed, restricted := foundations[from]; restricted && !allowed[to] {
				t.Errorf("foundation %s cannot depend on %s", from, to)
			}
			if independent[from] && to == "agent" {
				t.Errorf("%s must consume shared protocols instead of agent", from)
			}
			if from == "task" && (to == "team" || to == "worktree") {
				t.Errorf("task domain must not depend on %s", to)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
