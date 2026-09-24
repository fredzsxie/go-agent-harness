package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJournalPersistsAndRestoresCachedValues(t *testing.T) {
	store := NewStore(StoreConfig{WorkDir: t.TempDir()})
	runID, err := store.ReserveRun("review")
	if err != nil {
		t.Fatal(err)
	}
	key, err := SemanticKey("agent", "audit", "prompt", map[string]any{"type": "object"})
	if err != nil {
		t.Fatal(err)
	}
	journal, err := store.OpenJournal(runID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Record(key, map[string]any{"findings": []any{"one"}}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}

	resumed, err := store.OpenJournal(runID, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resumed.Close() })
	value, found, err := resumed.Cached(key)
	if err != nil || !found {
		t.Fatalf("expected cached journal value: %#v, %t, %v", value, found, err)
	}
	if value.(map[string]any)["findings"].([]any)[0] != "one" {
		t.Fatalf("unexpected cached value: %#v", value)
	}
}

func TestOpenJournalRejectsCorruptResumeData(t *testing.T) {
	root := t.TempDir()
	store := NewStore(StoreConfig{WorkDir: root})
	runID, err := store.ReserveRun("review")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, defaultStoreDir, runID+".journal.jsonl")
	if err := os.WriteFile(path, []byte("{bad json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenJournal(runID, true); err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("expected corrupt journal error, got %v", err)
	}
}

func TestSemanticKeyIsStableAndContentAddressed(t *testing.T) {
	first, err := SemanticKey("agent", "audit", "prompt", map[string]any{
		"properties": map[string]any{"b": map[string]any{"type": "string"}, "a": map[string]any{"type": "boolean"}},
		"type":       "object",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := SemanticKey("agent", "audit", "prompt", map[string]any{
		"type":       "object",
		"properties": map[string]any{"a": map[string]any{"type": "boolean"}, "b": map[string]any{"type": "string"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	changed, err := SemanticKey("agent", "audit", "changed", map[string]any{"type": "object"})
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first == changed {
		t.Fatalf("unexpected semantic keys: %s %s %s", first, second, changed)
	}
}
