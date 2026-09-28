package workflow

import (
	"bytes"
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestResumeValidationPreservesSuccessfulArtifacts(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	if err := registry.Register(Definition{
		Metadata: Metadata{Name: "original", Description: "original"},
		Script: func(_ context.Context, _ ExecutionContext, args map[string]any) (any, error) {
			args["value"] = "mutated by script"
			return map[string]any{"ok": true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(Definition{
		Metadata: Metadata{Name: "different", Description: "different"},
		Script:   func(context.Context, ExecutionContext, map[string]any) (any, error) { return nil, nil },
	}); err != nil {
		t.Fatal(err)
	}
	store := NewStore(StoreConfig{WorkDir: root})
	manager := NewManager(ManagerConfig{Registry: registry, Store: store, Runner: &countingRunner{}})
	result, err := manager.Run(context.Background(), ToolInput{
		Name: "original", Args: map[string]any{"value": "stable"}, HasArgs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshotPath := store.snapshotPath(result.Task.RunID)
	outputPath := store.outputPath(result.Task.RunID)
	originalSnapshot, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	originalOutput, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.ReadSnapshot(result.Task.RunID)
	if err != nil || snapshot.Args["value"] != "stable" {
		t.Fatalf("script mutated persisted args: %#v, %v", snapshot.Args, err)
	}

	invalidResumes := []ToolInput{
		{Name: "original", Args: map[string]any{"value": "changed"}, HasArgs: true, ResumeFromRunID: result.Task.RunID},
		{Name: "different", ResumeFromRunID: result.Task.RunID},
	}
	for _, input := range invalidResumes {
		if _, err := manager.Run(context.Background(), input); err == nil {
			t.Fatalf("expected invalid resume to fail: %#v", input)
		}
		currentSnapshot, readErr := os.ReadFile(snapshotPath)
		if readErr != nil || !bytes.Equal(currentSnapshot, originalSnapshot) {
			t.Fatalf("invalid resume changed snapshot: %v", readErr)
		}
		currentOutput, readErr := os.ReadFile(outputPath)
		if readErr != nil || !bytes.Equal(currentOutput, originalOutput) {
			t.Fatalf("invalid resume changed output: %v", readErr)
		}
	}
}

func TestConcurrentResumeIsRejectedAcrossStoreInstances(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	if err := registry.Register(Definition{
		Metadata: Metadata{Name: "blocking", Description: "blocking"},
		Script: func(context.Context, ExecutionContext, map[string]any) (any, error) {
			if calls.Add(1) > 1 {
				entered <- struct{}{}
				<-release
			}
			return map[string]any{"ok": true}, nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	firstManager := NewManager(ManagerConfig{
		Registry: registry, Store: NewStore(StoreConfig{WorkDir: root}), Runner: &countingRunner{},
	})
	first, err := firstManager.Run(context.Background(), ToolInput{Name: "blocking"})
	if err != nil {
		t.Fatal(err)
	}

	resumeDone := make(chan error, 1)
	go func() {
		_, err := firstManager.Run(context.Background(), ToolInput{Name: "blocking", ResumeFromRunID: first.Task.RunID})
		resumeDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("resume did not enter blocking workflow")
	}
	secondManager := NewManager(ManagerConfig{
		Registry: registry, Store: NewStore(StoreConfig{WorkDir: root}), Runner: &countingRunner{},
	})
	secondDone := make(chan error, 1)
	go func() {
		_, err := secondManager.Run(context.Background(), ToolInput{Name: "blocking", ResumeFromRunID: first.Task.RunID})
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		if err == nil {
			close(release)
			t.Fatal("expected concurrent resume to be rejected")
		}
	case <-time.After(time.Second):
		close(release)
		<-secondDone
		<-resumeDone
		t.Fatal("concurrent resume waited instead of being rejected")
	}
	close(release)
	select {
	case err := <-resumeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocking resume did not finish")
	}
}
