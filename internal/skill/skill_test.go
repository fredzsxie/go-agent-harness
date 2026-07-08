package skill

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFrontmatter(t *testing.T) {
	raw := "---\nname: code-review\ndescription: Review code carefully\n---\n# Code Review\nDetails"

	meta, body := parseFrontmatter(raw)
	if meta.Name != "code-review" {
		t.Fatalf("expected name code-review, got %q", meta.Name)
	}
	if meta.Description != "Review code carefully" {
		t.Fatalf("expected description, got %q", meta.Description)
	}
	if body != "# Code Review\nDetails" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestRunLoadReturnsSkillContentByName(t *testing.T) {
	manager := &Manager{
		entries: map[string]Entry{
			"code-review": {
				Name:        "code-review",
				Description: "Review code carefully",
				Content:     "# Code Review\nDetails",
			},
		},
		order: []string{"code-review"},
	}

	output, err := manager.RunLoad(context.Background(), map[string]any{"name": "code-review"})
	if err != nil {
		t.Fatalf("RunLoad error = %v", err)
	}
	if !strings.Contains(output, "Code Review") {
		t.Fatalf("expected full skill content, got %q", output)
	}
}

func TestScanBuildsCatalogFromSkillDir(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, skillsDirName, "sql-style")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir error = %v", err)
	}
	raw := "---\nname: sql-style\ndescription: SQL guide\n---\n# SQL Style\nUse uppercase keywords"
	if err := os.WriteFile(filepath.Join(skillDir, skillFileName), []byte(raw), 0o644); err != nil {
		t.Fatalf("write skill error = %v", err)
	}

	manager := &Manager{
		entries: make(map[string]Entry),
		order:   make([]string, 0, 4),
	}
	if err := manager.scan(filepath.Join(root, skillsDirName)); err != nil {
		t.Fatalf("scan error = %v", err)
	}

	catalog := manager.ListSkills()
	if !strings.Contains(catalog, "**sql-style**: SQL guide") {
		t.Fatalf("unexpected catalog: %q", catalog)
	}
}
