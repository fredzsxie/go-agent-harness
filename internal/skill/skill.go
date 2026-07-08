// Package skill 负责技能目录扫描、元数据目录构建和按名称加载 SKILL.md 全文，
// 用于实现“目录常驻 system prompt，正文按需通过 tool_result 注入”的两层技能加载。
package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"go-agent-harness/internal/workspace"
)

// s07: Skill Loading — two-level on-demand knowledge injection.
//
//   Layer 1 (cheap, always present):
//     SYSTEM prompt includes skill names + one-line descriptions (~100 tokens/skill)
//     "Skills available: agent-builder, code-review, mcp-builder, pdf"
//
//   Layer 2 (expensive, on demand):
//     Agent calls load_skill("code-review") → full SKILL.md content
//     injected via tool_result (~2000 tokens/skill)
//
//   skills/
//     agent-builder/SKILL.md
//     code-review/SKILL.md
//     mcp-builder/SKILL.md
//     pdf/SKILL.md

const (
	skillsDirName     = "skills"
	skillFileName     = "SKILL.md"
	emptySkillCatalog = "(no skills found)"
)

type Entry struct {
	Name        string
	Description string
	Content     string
}

type Manager struct {
	entries map[string]Entry
	order   []string
}

func New() *Manager {
	manager := &Manager{
		entries: make(map[string]Entry),
		order:   make([]string, 0, 8),
	}
	if err := manager.scan(filepath.Join(workspace.Root(), skillsDirName)); err != nil {
		panic(err)
	}
	return manager
}

func (m *Manager) ListSkills() string {
	if len(m.order) == 0 {
		return emptySkillCatalog
	}

	lines := make([]string, 0, len(m.order))
	for _, name := range m.order {
		entry := m.entries[name]
		lines = append(lines, fmt.Sprintf("- **%s**: %s", entry.Name, entry.Description))
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) RunLoad(_ context.Context, input any) (string, error) {
	payload, ok := input.(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid load_skill payload")
	}

	name, _ := payload["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("missing name")
	}

	entry, ok := m.entries[name]
	if !ok {
		return fmt.Sprintf("Skill not found: %s", name), nil
	}
	return entry.Content, nil
}

func (m *Manager) scan(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}

	for _, dirEntry := range entries {
		if !dirEntry.IsDir() {
			continue
		}

		manifestPath := filepath.Join(root, dirEntry.Name(), skillFileName)
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}

		meta, body := parseFrontmatter(string(raw))
		name := strings.TrimSpace(meta.Name)
		if name == "" {
			name = dirEntry.Name()
		}
		description := strings.TrimSpace(meta.Description)
		if description == "" {
			description = fallbackDescription(body)
		}

		m.entries[name] = Entry{
			Name:        name,
			Description: description,
			Content:     string(raw),
		}
		m.order = append(m.order, name)
	}

	return nil
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

func parseFrontmatter(raw string) (frontmatter, string) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(raw, "---\n") {
		return frontmatter{}, raw
	}

	parts := strings.SplitN(raw, "---\n", 3)
	if len(parts) < 3 {
		return frontmatter{}, raw
	}

	var meta frontmatter
	if err := yaml.Unmarshal([]byte(parts[1]), &meta); err != nil {
		return frontmatter{}, raw
	}
	return meta, strings.TrimSpace(parts[2])
}

func fallbackDescription(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "#")
		return strings.TrimSpace(line)
	}
	return "Skill"
}
