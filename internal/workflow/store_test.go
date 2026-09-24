package workflow

import (
	"path/filepath"
	"testing"
)

func TestStorePersistsSnapshotAndOutput(t *testing.T) {
	root := t.TempDir()
	store := NewStore(StoreConfig{WorkDir: root})
	runID, err := store.ReserveRun("review-changes")
	if err != nil {
		t.Fatal(err)
	}
	if !runIDPattern.MatchString(runID) {
		t.Fatalf("unexpected run ID: %s", runID)
	}

	release, err := store.LockRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.LockRun(runID); err == nil {
		t.Fatal("expected an active run to reject a second lock")
	}
	release()
	if releaseAgain, err := store.LockRun(runID); err != nil {
		t.Fatal(err)
	} else {
		releaseAgain()
	}

	snapshot := Snapshot{
		RunID: runID, WorkflowName: "review-changes", Args: map[string]any{"changes": "diff"},
		Task: Task{TaskID: "local_" + runID, TaskType: TaskType, RunID: runID, Workflow: "review-changes", Status: StatusRunning},
	}
	if err := store.WriteSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.ReadSnapshot(runID)
	if err != nil || loaded.WorkflowName != snapshot.WorkflowName || loaded.Args["changes"] != "diff" {
		t.Fatalf("unexpected snapshot: %#v, %v", loaded, err)
	}
	outputPath, err := store.WriteOutput(runID, map[string]any{"ok": true})
	if err != nil {
		t.Fatal(err)
	}
	if outputPath != filepath.Join(".workflows", runID+".output.json") {
		t.Fatalf("unexpected output path: %s", outputPath)
	}
}

func TestStoreRejectsEscapesAndInvalidRunIDs(t *testing.T) {
	root := t.TempDir()
	store := NewStore(StoreConfig{WorkDir: root, Dir: "../outside"})
	if _, err := store.ReserveRun("review"); err == nil {
		t.Fatal("expected escaped store directory to fail")
	}

	validStore := NewStore(StoreConfig{WorkDir: root})
	if _, err := validStore.LockRun("../../escape"); err == nil {
		t.Fatal("expected invalid run ID to fail")
	}
	if _, err := validStore.ReserveRun("../review"); err == nil {
		t.Fatal("expected invalid workflow name to fail")
	}
}
