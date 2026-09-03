package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go-agent-harness/internal/task"
)

func TestCreateResolveAndRemove(t *testing.T) {
	root := gitRepository(t)
	tasks := task.New(task.Config{WorkDir: root})
	created, err := tasks.Create("Implement feature", "")
	if err != nil {
		t.Fatal(err)
	}
	manager := New(Config{WorkDir: root, Tasks: tasks})

	path, err := manager.Create(context.Background(), "feature-a", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := tasks.Get(created.ID)
	if err != nil || stored.Worktree == nil || *stored.Worktree != "feature-a" {
		t.Fatalf("task binding missing: %#v, %v", stored, err)
	}
	resolved, err := manager.Resolve(context.Background(), stored)
	if err != nil || resolved.Root() != path {
		t.Fatalf("unexpected resolver: %#v, %v", resolved, err)
	}
	if branch := git(t, path, "branch", "--show-current"); branch != "wt/feature-a" {
		t.Fatalf("unexpected branch: %s", branch)
	}

	if err := os.WriteFile(filepath.Join(path, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Claim(created.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tasks.Complete(created.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), "feature-a", false); err == nil {
		t.Fatal("dirty worktree must require discard")
	}
	if err := manager.Remove(context.Background(), "feature-a", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("worktree path should be removed, got %v", err)
	}
	stored, err = tasks.Get(created.ID)
	if err != nil || stored.Worktree != nil {
		t.Fatalf("task binding should be cleared: %#v, %v", stored, err)
	}
	if branches := git(t, root, "branch", "--list", "wt/feature-a"); branches == "" {
		t.Fatal("worktree branch should be retained")
	}
}

func TestResolveWithoutBindingUsesMainWorkspace(t *testing.T) {
	root := gitRepository(t)
	tasks := task.New(task.Config{WorkDir: root})
	created, err := tasks.Create("Main workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := New(Config{WorkDir: root, Tasks: tasks}).Resolve(context.Background(), created)
	expected, resolveErr := filepath.EvalSymlinks(root)
	if err != nil || resolveErr != nil || resolved.Root() != expected {
		t.Fatalf("unexpected root resolver: %#v, %v", resolved, err)
	}
}

func TestResolveWithoutBindingDoesNotRequireGit(t *testing.T) {
	root := t.TempDir()
	tasks := task.New(task.Config{WorkDir: root})
	created, err := tasks.Create("Non-Git workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := New(Config{WorkDir: root, Tasks: tasks}).Resolve(context.Background(), created)
	if err != nil {
		t.Fatalf("unbound task should use a non-Git workspace: %v", err)
	}
	expected, err := filepath.EvalSymlinks(root)
	if err != nil || resolved.Root() != expected {
		t.Fatalf("unexpected workspace root: %s, %v", resolved.Root(), err)
	}
}

func TestBrokenBindingFailsClosed(t *testing.T) {
	root := gitRepository(t)
	tasks := task.New(task.Config{WorkDir: root})
	created, err := tasks.Create("Broken binding", "")
	if err != nil {
		t.Fatal(err)
	}
	bound, err := tasks.BindWorktree(created.ID, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{WorkDir: root, Tasks: tasks}).Resolve(context.Background(), bound); err == nil {
		t.Fatal("broken binding must not fall back to the main workspace")
	}
}

func TestCreateValidatesNameTaskAndLease(t *testing.T) {
	root := gitRepository(t)
	tasks := task.New(task.Config{WorkDir: root})
	created, err := tasks.Create("Validation", "")
	if err != nil {
		t.Fatal(err)
	}
	manager := New(Config{WorkDir: root, Tasks: tasks})
	if _, err := manager.Create(context.Background(), "../escape", created.ID); err == nil {
		t.Fatal("unsafe name should be rejected")
	}
	if _, err := manager.Create(context.Background(), "valid", "task_deadbeef"); err == nil {
		t.Fatal("missing task should be rejected")
	}

	path, err := manager.Create(context.Background(), "leased", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Claim(created.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tasks.Complete(created.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	leased := New(Config{WorkDir: root, Tasks: tasks, InUse: func(candidate string) bool {
		return candidate == path
	}})
	if err := leased.Remove(context.Background(), "leased", true); err == nil {
		t.Fatal("active lease should prevent removal")
	}
}

func gitRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", "initial")
	return root
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %s", strings.Join(args, " "), output)
	}
	return strings.TrimSpace(string(output))
}
