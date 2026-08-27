package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistentTaskLifecycleAndUnblocking(t *testing.T) {
	workDir := t.TempDir()
	manager := New(Config{WorkDir: workDir})
	first, err := manager.Create("Read files", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create("Summarize README", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddBlockedBy(second.ID, []string{first.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Claim(second.ID, AgentOwner); err == nil {
		t.Fatal("blocked task should not be claimable")
	}
	if _, err := manager.Claim(first.ID, AgentOwner); err != nil {
		t.Fatal(err)
	}
	completed, unblocked, err := manager.Complete(first.ID, AgentOwner)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != Completed || len(unblocked) != 1 || unblocked[0].ID != second.ID {
		t.Fatalf("unexpected completion result: %#v, %#v", completed, unblocked)
	}

	reloaded := New(Config{WorkDir: workDir})
	stored, err := reloaded.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != Completed || stored.Owner == nil || *stored.Owner != AgentOwner {
		t.Fatalf("task was not persisted: %#v", stored)
	}
	if _, err := reloaded.Claim(second.ID, AgentOwner); err != nil {
		t.Fatalf("newly unblocked task should be claimable: %v", err)
	}
}

func TestDependenciesAreIdempotentAndCyclesAreRejected(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})
	first, err := manager.Create("First", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create("Second", "")
	if err != nil {
		t.Fatal(err)
	}

	updated, err := manager.AddBlockedBy(second.ID, []string{first.ID, first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.BlockedBy) != 1 || updated.BlockedBy[0] != first.ID {
		t.Fatalf("dependencies should be deduplicated: %#v", updated.BlockedBy)
	}
	if _, err := manager.AddBlockedBy(first.ID, []string{second.ID}); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
	unchanged, err := manager.Get(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unchanged.BlockedBy) != 0 {
		t.Fatalf("failed update must not mutate task: %#v", unchanged)
	}
}

func TestTaskStateAndOwnerAreEnforced(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})
	task, err := manager.Create("Owned work", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Complete(task.ID, AgentOwner); err == nil {
		t.Fatal("pending task should not be completed")
	}
	if _, err := manager.Claim(task.ID, AgentOwner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Complete(task.ID, "other"); err == nil {
		t.Fatal("a different owner should not complete the task")
	}
	if _, err := manager.AddBlockedBy(task.ID, []string{task.ID}); err == nil {
		t.Fatal("claimed task should not be updated")
	}
}

func TestTaskDirectoryCannotEscapeWorkspace(t *testing.T) {
	workDir := t.TempDir()
	manager := New(Config{WorkDir: workDir, TaskDir: filepath.Join(workDir, "..", "outside")})
	if _, err := manager.Create("Unsafe", ""); err == nil {
		t.Fatal("task directory outside workspace should be rejected")
	}
	if _, err := os.Stat(filepath.Join(workDir, "..", "outside")); !os.IsNotExist(err) {
		t.Fatalf("outside directory should not be created, got %v", err)
	}
}
