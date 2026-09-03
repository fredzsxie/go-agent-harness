package task

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestConcurrentClaimHasSingleWinner(t *testing.T) {
	workDir := t.TempDir()
	created, err := New(Config{WorkDir: workDir}).Create("Shared work", "")
	if err != nil {
		t.Fatal(err)
	}

	const contenders = 16
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wait sync.WaitGroup
	for i := range contenders {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, err := New(Config{WorkDir: workDir}).Claim(created.ID, fmt.Sprintf("worker-%d", index))
			results <- err
		}(i)
	}
	close(start)
	wait.Wait()
	close(results)

	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("expected one claim winner, got %d", winners)
	}
	stored, err := New(Config{WorkDir: workDir}).Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != InProgress || stored.Owner == nil {
		t.Fatalf("winning claim was not persisted: %#v", stored)
	}
}

func TestOwnerCanHoldOnlyOneInProgressTask(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})
	first, err := manager.Create("First", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create("Second", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Claim(first.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Claim(second.ID, "alice"); err == nil || !strings.Contains(err.Error(), "already has") {
		t.Fatalf("expected owner assignment conflict, got %v", err)
	}
	if _, _, err := manager.Complete(first.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Claim(second.ID, "alice"); err != nil {
		t.Fatalf("completed assignment should allow another claim: %v", err)
	}
}

func TestClaimNextSkipsBlockedTask(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})
	first, err := manager.Create("First", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create("Second", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AddBlockedBy(second.ID, []string{first.ID}); err != nil {
		t.Fatal(err)
	}

	claimed, ok, err := manager.ClaimNext("alice")
	if err != nil || !ok || claimed.ID != first.ID {
		t.Fatalf("expected first ready task, got %#v, %v, %v", claimed, ok, err)
	}
	if _, _, err := manager.Complete(first.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err = manager.ClaimNext("bob")
	if err != nil || !ok || claimed.ID != second.ID {
		t.Fatalf("expected newly unblocked task, got %#v, %v, %v", claimed, ok, err)
	}
}

func TestReleaseOwnerReturnsTaskToBoard(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})
	created, err := manager.Create("Recoverable work", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Claim(created.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	released, err := manager.ReleaseOwner("alice")
	if err != nil || !released {
		t.Fatalf("release failed: %v, %v", released, err)
	}
	stored, err := manager.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != Pending || stored.Owner != nil {
		t.Fatalf("released task should be pending and unowned: %#v", stored)
	}
}

func TestWorktreeBindingLifecycle(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})
	first, err := manager.Create("Isolated work", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create("Other work", "")
	if err != nil {
		t.Fatal(err)
	}

	bound, err := manager.BindWorktree(first.ID, "feature-a")
	if err != nil || bound.Worktree == nil || *bound.Worktree != "feature-a" {
		t.Fatalf("bind failed: %#v, %v", bound, err)
	}
	if _, err := manager.BindWorktree(second.ID, "feature-a"); err == nil {
		t.Fatal("one worktree must not be bound to multiple tasks")
	}
	if _, err := manager.ClearWorktree(first.ID, "feature-a"); err == nil {
		t.Fatal("pending task binding must not be cleared")
	}
	if _, err := manager.Claim(first.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.Complete(first.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	cleared, err := manager.ClearWorktree(first.ID, "feature-a")
	if err != nil || cleared.Worktree != nil {
		t.Fatalf("clear failed: %#v, %v", cleared, err)
	}
}

func TestClaimedTaskCannotBindWorktree(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})
	created, err := manager.Create("Owned work", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Claim(created.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.BindWorktree(created.ID, "feature-a"); err == nil {
		t.Fatal("claimed task must not accept a worktree binding")
	}
}
