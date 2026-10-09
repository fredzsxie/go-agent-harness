package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go-agent-harness/internal/workspace"
)

func TestGlobSupportsRecursiveDoubleStarAndCapsResults(t *testing.T) {
	root := t.TempDir()
	resolver, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	tools := New(resolver)
	testDir := filepath.Join(root, "fixture")
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		t.Fatal(err)
	}

	relRoot, err := filepath.Rel(root, testDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(testDir, "one", "two"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root.py", "one/one.py", "one/two/deep.py"} {
		if err := os.WriteFile(filepath.Join(testDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	output, err := tools.RunGlob(context.Background(), map[string]any{"pattern": filepath.ToSlash(filepath.Join(relRoot, "**", "*.py"))})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root.py", "one/one.py", "one/two/deep.py"} {
		if !strings.Contains(output, filepath.ToSlash(filepath.Join(relRoot, name))) {
			t.Fatalf("missing recursive match %s in %q", name, output)
		}
	}

	for i := 0; i < 205; i++ {
		if err := os.WriteFile(filepath.Join(testDir, fmt.Sprintf("file-%03d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	output, err = tools.RunGlob(context.Background(), map[string]any{"pattern": filepath.ToSlash(filepath.Join(relRoot, "*.txt"))})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(output, "\n")
	if len(lines) != 201 || lines[200] != "... (more matches omitted; narrow the pattern)" {
		t.Fatalf("unexpected capped glob output: %d lines, tail %q", len(lines), lines[len(lines)-1])
	}
}

func TestReadFileSupportsUTF8AndLineLimit(t *testing.T) {
	root := t.TempDir()
	resolver, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	tools := New(resolver)
	testDir := filepath.Join(root, "fixture")
	if err := os.MkdirAll(testDir, 0o755); err != nil {
		t.Fatal(err)
	}
	relPath, err := filepath.Rel(root, filepath.Join(testDir, "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "note.txt"), []byte("你好\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := tools.RunReadFile(context.Background(), map[string]any{"path": relPath, "limit": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if output != "你好\nsecond\n... (1 more lines)" {
		t.Fatalf("unexpected limited read: %q", output)
	}
}

func TestToolsUseBoundWorkspace(t *testing.T) {
	root := t.TempDir()
	resolver, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	tools := New(resolver)
	if _, err := tools.RunWriteFile(context.Background(), map[string]any{"path": "note.txt", "content": "bound"}); err != nil {
		t.Fatal(err)
	}
	output, err := tools.RunReadFile(context.Background(), map[string]any{"path": "note.txt"})
	if err != nil || output != "bound" {
		t.Fatalf("unexpected bound read: %q, %v", output, err)
	}
	pwd, err := tools.RunBash(context.Background(), map[string]any{"command": "pwd"})
	if err != nil || strings.TrimSpace(pwd) != resolver.Root() {
		t.Fatalf("bash cwd = %q, %v; want %q", pwd, err, resolver.Root())
	}
	if _, err := tools.RunReadFile(context.Background(), map[string]any{"path": "../outside.txt"}); err == nil {
		t.Fatal("bound tools should reject paths outside their workspace")
	}
}

func TestDynamicToolsResolveWorkspacePerCall(t *testing.T) {
	first, err := workspace.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	second, err := workspace.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	current := first
	tools := NewDynamic(func() (*workspace.Resolver, error) { return current, nil })
	if _, err := tools.RunWriteFile(context.Background(), map[string]any{"path": "first.txt", "content": "one"}); err != nil {
		t.Fatal(err)
	}
	current = second
	if _, err := tools.RunWriteFile(context.Background(), map[string]any{"path": "second.txt", "content": "two"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(first.Root(), "first.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(second.Root(), "second.txt")); err != nil {
		t.Fatal(err)
	}
}
