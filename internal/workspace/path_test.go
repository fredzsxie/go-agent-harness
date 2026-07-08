// Package workspace 的测试覆盖工作区路径解析的边界场景，
// 重点验证缺失文件、越界路径与符号链接逃逸的处理结果。
package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolverResolveAllowsNestedMissingPathInsideWorkspace(t *testing.T) {
	root := t.TempDir()
	resolver, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resolved, err := resolver.Resolve(filepath.Join("nested", "file.txt"))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	expected := filepath.Join(resolver.Root(), "nested", "file.txt")
	if resolved != expected {
		t.Fatalf("expected %q, got %q", expected, resolved)
	}
}

func TestResolverResolveRejectsPathOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	resolver, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := resolver.Resolve("../outside.txt"); err == nil {
		t.Fatal("expected Resolve() to reject escaped path")
	}
}

func TestResolverResolveRejectsSymlinkEscapeForMissingLeaf(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	linkPath := filepath.Join(root, "link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	resolver, err := New(root)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := resolver.Resolve(filepath.Join("link", "new.txt")); err == nil {
		t.Fatal("expected Resolve() to reject symlink escape")
	}
}
