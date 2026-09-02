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
	root := workspace.Root()
	testDir, err := os.MkdirTemp(root, ".glob-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testDir)

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

	output, err := RunGlob(context.Background(), map[string]any{"pattern": filepath.ToSlash(filepath.Join(relRoot, "**", "*.py"))})
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
	output, err = RunGlob(context.Background(), map[string]any{"pattern": filepath.ToSlash(filepath.Join(relRoot, "*.txt"))})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(output, "\n")
	if len(lines) != 201 || lines[200] != "... (more matches omitted; narrow the pattern)" {
		t.Fatalf("unexpected capped glob output: %d lines, tail %q", len(lines), lines[len(lines)-1])
	}
}

func TestReadFileSupportsUTF8AndLineLimit(t *testing.T) {
	root := workspace.Root()
	testDir, err := os.MkdirTemp(root, ".read-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(testDir)
	relPath, err := filepath.Rel(root, filepath.Join(testDir, "note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testDir, "note.txt"), []byte("你好\nsecond\nthird\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	output, err := RunReadFile(context.Background(), map[string]any{"path": relPath, "limit": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if output != "你好\nsecond\n... (1 more lines)" {
		t.Fatalf("unexpected limited read: %q", output)
	}
}
