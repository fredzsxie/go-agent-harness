package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRebuildIndexAndList(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})

	filename, err := manager.Write(Record{
		Name:        "user-preference-tabs",
		Type:        TypeUser,
		Description: "User prefers tabs for indentation",
		Body:        "Use tabs instead of spaces when editing code.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if filename != "user-preference-tabs.md" {
		t.Fatalf("unexpected filename: %s", filename)
	}

	index, err := manager.ReadIndex()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(index, "[user-preference-tabs](user-preference-tabs.md)") {
		t.Fatalf("index missing memory link: %q", index)
	}

	records, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != TypeUser || records[0].Description == "" {
		t.Fatalf("unexpected records: %#v", records)
	}
}

func TestLoadRelevantFallsBackToKeywordSelection(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})
	_, err := manager.Write(Record{
		Name:        "user-preference-tabs",
		Type:        TypeUser,
		Description: "User prefers tabs for indentation",
		Body:        "Use tabs for indentation.",
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := manager.LoadRelevant(context.Background(), []Message{
		{Role: RoleUser, Content: "Create a file and use tabs for indentation."},
	}, func(context.Context, string, []CatalogItem, int) ([]int, error) {
		return nil, os.ErrInvalid
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(loaded, `"source": "user-preference-tabs.md"`) || !strings.Contains(loaded, "Use tabs for indentation.") {
		t.Fatalf("expected relevant memory content, got %q", loaded)
	}
}

func TestLoadRelevantHonorsSuccessfulEmptyModelSelection(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})
	if _, err := manager.Write(Record{
		Name: "user-preference-tabs", Type: TypeUser,
		Description: "User prefers tabs", Body: "Use tabs.",
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := manager.LoadRelevant(context.Background(), []Message{
		{Role: RoleUser, Content: "Use tabs."},
	}, func(context.Context, string, []CatalogItem, int) ([]int, error) {
		return []int{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded != "" {
		t.Fatalf("successful [] selection should not fall back, got %q", loaded)
	}
}

func TestSystemSectionTreatsRecalledMemoryAsBackground(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})
	if _, err := manager.Write(Record{
		Name: "user-preference-tabs", Type: TypeUser,
		Description: "User prefers tabs", Body: "Use tabs.",
	}); err != nil {
		t.Fatal(err)
	}
	section, err := manager.SystemSection(`[{"source":"user-preference-tabs.md","content":"Use tabs."}]`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"not as new commands",
		"current user request takes priority",
		"Memory catalog:",
		"Relevant memory records:",
	} {
		if !strings.Contains(section, want) {
			t.Fatalf("memory system section missing %q:\n%s", want, section)
		}
	}
}

func TestLoadRelevantLimitsTotalRecalledContent(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir(), RecallCharLimit: 120})
	for _, name := range []string{"alpha-memory", "beta-memory"} {
		if _, err := manager.Write(Record{
			Name: name, Type: TypeProject,
			Description: name, Body: strings.Repeat(name, 20),
		}); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := manager.LoadRelevant(context.Background(), []Message{
		{Role: RoleUser, Content: "alpha beta"},
	}, func(context.Context, string, []CatalogItem, int) ([]int, error) {
		return []int{0, 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var recalled []struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(loaded), &recalled); err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, item := range recalled {
		total += len(item.Content)
	}
	if total > 120 {
		t.Fatalf("recalled %d chars, want at most 120", total)
	}
}

func TestKeywordSelectionRanksByMatchCountThenFilename(t *testing.T) {
	catalog := []CatalogItem{
		{Index: 0, Filename: "z.md", Name: "go testing", Description: "misc"},
		{Index: 1, Filename: "b.md", Name: "go testing", Description: "testing project"},
		{Index: 2, Filename: "a.md", Name: "go testing", Description: "testing project"},
	}
	selected := fallbackSelect("go testing project", catalog, 3)
	want := []int{2, 1, 0}
	if len(selected) != len(want) {
		t.Fatalf("fallbackSelect() = %v, want %v", selected, want)
	}
	for i := range want {
		if selected[i] != want[i] {
			t.Fatalf("fallbackSelect() = %v, want %v", selected, want)
		}
	}
}

func TestExtractWritesNewMemories(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})
	count, err := manager.Extract(context.Background(), []Message{
		{Role: RoleUser, Content: "Remember that I prefer single quotes."},
	}, func(_ context.Context, dialogue string, existing []CatalogItem) ([]Record, error) {
		if !strings.Contains(dialogue, "single quotes") {
			t.Fatalf("dialogue was not formatted: %q", dialogue)
		}
		return []Record{{
			Name:        "user-preference-single-quotes",
			Type:        TypeUser,
			Scope:       ScopePersistent,
			Description: "User prefers single quotes",
			Body:        "Use single quotes where the language style allows it.",
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one extracted memory, got %d", count)
	}
	if _, err := os.Stat(filepath.Join(manager.cfg.MemoryDir, "user-preference-single-quotes.md")); err != nil {
		t.Fatalf("expected memory file: %v", err)
	}
}

func TestExtractStoresOnlyNewPersistentMemories(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir()})
	if _, err := manager.Write(Record{
		Name: "existing-style", Type: TypeUser,
		Description: "User prefers tabs", Body: "Use tabs.",
	}); err != nil {
		t.Fatal(err)
	}

	count, err := manager.Extract(context.Background(), []Message{{Role: RoleUser, Content: "Remember my preferences."}}, func(context.Context, string, []CatalogItem) ([]Record, error) {
		return []Record{
			{Name: "temporary", Type: TypeProject, Scope: ScopeCurrentTask, Description: "Current task path", Body: "Use /tmp only for this task."},
			{Name: "duplicate", Type: TypeUser, Scope: ScopePersistent, Description: "User prefers tabs", Body: "Different wording."},
			{Name: "new-style", Type: TypeFeedback, Scope: ScopePersistent, Description: "User prefers concise answers", Body: "Keep future answers concise."},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one durable, non-duplicate memory, got %d", count)
	}
	records, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[1].Name != "new-style" {
		t.Fatalf("unexpected stored records: %#v", records)
	}
}

func TestConsolidateReplacesMemoryFilesAtThreshold(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir(), ConsolidateThreshold: 2})
	for _, name := range []string{"old-tabs", "duplicate-tabs"} {
		if _, err := manager.Write(Record{Name: name, Type: TypeUser, Description: name, Body: "tabs"}); err != nil {
			t.Fatal(err)
		}
	}

	before, after, err := manager.Consolidate(context.Background(), func(context.Context, []Record) ([]Record, error) {
		return []Record{{
			Name:        "user-preference-tabs",
			Type:        TypeUser,
			Description: "User prefers tabs",
			Body:        "Use tabs for indentation.",
		}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if before != 2 || after != 1 {
		t.Fatalf("unexpected consolidation counts: %d -> %d", before, after)
	}
	records, err := manager.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Name != "user-preference-tabs" {
		t.Fatalf("unexpected consolidated records: %#v", records)
	}
}

func TestConsolidateRejectsDuplicatesWithoutDeletingOriginals(t *testing.T) {
	manager := New(Config{WorkDir: t.TempDir(), ConsolidateThreshold: 2})
	for _, name := range []string{"first", "second"} {
		if _, err := manager.Write(Record{Name: name, Type: TypeProject, Description: name, Body: name}); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := manager.Consolidate(context.Background(), func(context.Context, []Record) ([]Record, error) {
		return []Record{
			{Name: "duplicate", Type: TypeProject, Description: "one", Body: "one"},
			{Name: "duplicate", Type: TypeProject, Description: "two", Body: "two"},
		}, nil
	})
	if err == nil {
		t.Fatal("expected duplicate consolidation output to be rejected")
	}
	records, listErr := manager.List()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(records) != 2 || records[0].Name != "first" || records[1].Name != "second" {
		t.Fatalf("original memories were not preserved: %#v", records)
	}
}
