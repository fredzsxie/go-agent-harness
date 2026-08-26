// Package memory implements persistent cross-session memory.
//
// Memories are stored as Markdown files under .memory/. Each file has YAML
// frontmatter and MEMORY.md is rebuilt as a lightweight index that can be kept
// in the system prompt. Model-backed selection, extraction, and consolidation
// are injected by callers so this package stays independent from any LLM SDK.
package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"go-agent-harness/internal/conversation"
)

const (
	DefaultMaxRelevant          = 5  // 最多注入相关记忆数
	DefaultConsolidateThreshold = 10 // 触发合并的文件数阈值
	DefaultMaxFileBytes         = 4096
)

type Role = conversation.Role

const (
	RoleUser      = conversation.RoleUser
	RoleAssistant = conversation.RoleAssistant
)

type BlockType = conversation.BlockType

const (
	BlockText       = conversation.BlockText
	BlockToolUse    = conversation.BlockToolUse
	BlockToolResult = conversation.BlockToolResult
)

type ContentBlock = conversation.ContentBlock

type Message = conversation.Message

type Type string

const (
	TypeUser      Type = "user"
	TypeFeedback  Type = "feedback"
	TypeProject   Type = "project"
	TypeReference Type = "reference"
)

type Scope string

const (
	ScopePersistent  Scope = "persistent"
	ScopeCurrentTask Scope = "current_task"
)

type Record struct {
	Filename    string `json:"filename,omitempty"`
	Name        string `json:"name"`
	Type        Type   `json:"type"`
	Scope       Scope  `json:"scope,omitempty"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

type CatalogItem struct {
	Index       int
	Filename    string
	Name        string
	Description string
	Type        Type
}

type Selector func(ctx context.Context, recent string, catalog []CatalogItem, maxItems int) ([]int, error)

type Extractor func(ctx context.Context, dialogue string, existing []CatalogItem) ([]Record, error)

type Consolidator func(ctx context.Context, records []Record) ([]Record, error)

type Config struct {
	WorkDir              string
	MemoryDir            string
	MaxRelevant          int
	ConsolidateThreshold int
	MaxFileBytes         int
}

type Manager struct {
	cfg Config
}

func New(cfg Config) *Manager {
	if cfg.WorkDir == "" {
		cfg.WorkDir = "."
	}
	if cfg.MemoryDir == "" {
		cfg.MemoryDir = filepath.Join(cfg.WorkDir, ".memory")
	}
	if cfg.MaxRelevant <= 0 {
		cfg.MaxRelevant = DefaultMaxRelevant
	}
	if cfg.ConsolidateThreshold <= 0 {
		cfg.ConsolidateThreshold = DefaultConsolidateThreshold
	}
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = DefaultMaxFileBytes
	}
	return &Manager{cfg: cfg}
}

func (m *Manager) ReadIndex() (string, error) {
	raw, err := os.ReadFile(m.indexPath())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func (m *Manager) SystemSection() (string, error) {
	index, err := m.ReadIndex()
	if err != nil || index == "" {
		return "", err
	}
	return "Memories available:\n" + index + "\nRelevant memories may be injected into the current turn. Respect stable user preferences from memory.", nil
}

func (m *Manager) Write(record Record) (string, error) {
	if strings.TrimSpace(record.Name) == "" {
		return "", fmt.Errorf("memory name is required")
	}
	if strings.TrimSpace(record.Description) == "" {
		return "", fmt.Errorf("memory description is required")
	}
	if strings.TrimSpace(record.Body) == "" {
		return "", fmt.Errorf("memory body is required")
	}
	if !isValidType(record.Type) {
		return "", fmt.Errorf("unknown memory type: %s", record.Type)
	}
	if record.Scope == "" {
		record.Scope = ScopePersistent
	}

	if err := os.MkdirAll(m.cfg.MemoryDir, 0o755); err != nil {
		return "", err
	}
	filename := slugify(record.Name) + ".md"
	record.Filename = filename

	raw, err := marshalRecord(record)
	if err != nil {
		return "", err
	}
	path := filepath.Join(m.cfg.MemoryDir, filename)
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		return "", err
	}
	if err := m.RebuildIndex(); err != nil {
		return "", err
	}
	return filename, nil
}

// 获取 .memory/ 下的文件信息（按 Filename 排序）
func (m *Manager) List() ([]Record, error) {
	entries, err := os.ReadDir(m.cfg.MemoryDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "MEMORY.md" || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		record, err := m.read(entry.Name())
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Filename < records[j].Filename
	})
	return records, nil
}

func (m *Manager) RebuildIndex() error {
	records, err := m.List()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.cfg.MemoryDir, 0o755); err != nil {
		return err
	}

	lines := make([]string, 0, len(records))
	for _, record := range records {
		lines = append(lines, fmt.Sprintf("- [%s](%s) - %s", record.Name, record.Filename, record.Description))
	}
	text := strings.Join(lines, "\n")
	if text != "" {
		text += "\n"
	}
	return os.WriteFile(m.indexPath(), []byte(text), 0o644)
}

// 根据最近对话内容，调用LLM获取与对话可能相关的memory内容
func (m *Manager) LoadRelevant(ctx context.Context, messages []Message, selectRelevant Selector) (string, error) {
	records, err := m.List()
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}

	recent := RecentUserText(messages, 3, 2000)
	if strings.TrimSpace(recent) == "" {
		return "", nil
	}

	catalog := toCatalog(records)
	indices, err := selectRelevant(ctx, recent, catalog, m.cfg.MaxRelevant)
	if err != nil || len(indices) == 0 {
		indices = fallbackSelect(recent, catalog, m.cfg.MaxRelevant)
	}
	if len(indices) == 0 {
		return "", nil
	}

	parts := []string{"<relevant_memories>"}
	seen := map[int]bool{}
	for _, idx := range indices {
		if idx < 0 || idx >= len(records) || seen[idx] {
			continue
		}
		seen[idx] = true
		content, err := m.readContent(records[idx].Filename)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(content) != "" {
			parts = append(parts, content)
		}
		if len(seen) >= m.cfg.MaxRelevant {
			break
		}
	}
	if len(parts) == 1 {
		return "", nil
	}
	parts = append(parts, "</relevant_memories>")
	return strings.Join(parts, "\n\n"), nil
}

// 抽取最近10条记录（不超过4000个字符），调用LLM判断是否有需要提取为memory的内容
func (m *Manager) Extract(ctx context.Context, messages []Message, extract Extractor) (int, error) {
	dialogue := FormatRecentMessages(messages, 10, 4000)
	if strings.TrimSpace(dialogue) == "" {
		return 0, nil
	}
	existing, err := m.List()
	if err != nil {
		return 0, err
	}
	records, err := extract(ctx, dialogue, toCatalog(existing))
	if err != nil {
		return 0, err
	}

	count := 0
	for _, record := range records {
		if !shouldStoreMemory(record, existing) {
			continue
		}
		if _, err := m.Write(record); err != nil {
			return count, err
		}
		existing = append(existing, record)
		count++
	}
	return count, nil
}

// 判断文件数是否超过阈值。若超过阈值，调用 LLM side query 进行合并
func (m *Manager) Consolidate(ctx context.Context, consolidate Consolidator) (int, int, error) {
	records, err := m.List()
	if err != nil {
		return 0, 0, err
	}
	if len(records) < m.cfg.ConsolidateThreshold {
		return len(records), len(records), nil
	}
	next, err := consolidate(ctx, records)
	if err != nil {
		return len(records), len(records), err
	}
	if len(next) == 0 {
		return len(records), len(records), nil
	}

	if err := m.removeRecords(records); err != nil {
		return len(records), 0, err
	}
	count := 0
	for _, record := range next {
		if strings.TrimSpace(record.Name) == "" || strings.TrimSpace(record.Description) == "" || strings.TrimSpace(record.Body) == "" {
			continue
		}
		if _, err := m.Write(record); err != nil {
			return len(records), count, err
		}
		count++
	}
	if err := m.RebuildIndex(); err != nil {
		return len(records), count, err
	}
	return len(records), count, nil
}

func RecentUserText(messages []Message, maxItems int, maxChars int) string {
	if maxItems <= 0 {
		maxItems = 3
	}
	var parts []string
	for i := len(messages) - 1; i >= 0 && len(parts) < maxItems; i-- {
		if messages[i].Role != RoleUser {
			continue
		}
		text := messageText(messages[i])
		if strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	reverse(parts)
	out := strings.Join(parts, "\n")
	if maxChars > 0 && len(out) > maxChars {
		out = out[len(out)-maxChars:]
	}
	return out
}

func FormatRecentMessages(messages []Message, maxItems int, maxChars int) string {
	if maxItems <= 0 || maxItems > len(messages) {
		maxItems = len(messages)
	}
	start := len(messages) - maxItems
	if start < 0 {
		start = 0
	}

	parts := make([]string, 0, len(messages)-start)
	for _, message := range messages[start:] {
		text := messageText(message)
		if strings.TrimSpace(text) == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", message.Role, text))
	}
	out := strings.Join(parts, "\n")
	if maxChars > 0 && len(out) > maxChars {
		out = out[:maxChars]
	}
	return out
}

func (m *Manager) indexPath() string {
	return filepath.Join(m.cfg.MemoryDir, "MEMORY.md")
}

func (m *Manager) read(filename string) (Record, error) {
	content, err := m.readContent(filename)
	if err != nil {
		return Record{}, err
	}
	record, err := parseRecord(filename, content)
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

func (m *Manager) readContent(filename string) (string, error) {
	path := filepath.Join(m.cfg.MemoryDir, filepath.Base(filename))
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(raw) > m.cfg.MaxFileBytes {
		raw = raw[:m.cfg.MaxFileBytes]
	}
	return string(raw), nil
}

func (m *Manager) removeRecords(records []Record) error {
	for _, record := range records {
		if record.Filename == "" {
			continue
		}
		if err := os.Remove(filepath.Join(m.cfg.MemoryDir, filepath.Base(record.Filename))); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        Type   `yaml:"type"`
}

func parseRecord(filename string, content string) (Record, error) {
	meta, body := splitFrontmatter(content)
	record := Record{Filename: filename, Body: strings.TrimSpace(body)}
	if meta == "" {
		record.Name = strings.TrimSuffix(filename, filepath.Ext(filename))
		record.Description = firstLine(record.Body)
		record.Type = TypeUser
		record.Scope = ScopePersistent
		return record, nil
	}

	var fm frontmatter
	if err := yaml.Unmarshal([]byte(meta), &fm); err != nil {
		return Record{}, err
	}
	record.Name = fallback(fm.Name, strings.TrimSuffix(filename, filepath.Ext(filename)))
	record.Description = fallback(fm.Description, firstLine(record.Body))
	record.Type = normalizeType(fm.Type)
	record.Scope = ScopePersistent
	return record, nil
}

func marshalRecord(record Record) (string, error) {
	fm := frontmatter{
		Name:        record.Name,
		Description: record.Description,
		Type:        normalizeType(record.Type),
	}
	raw, err := yaml.Marshal(fm)
	if err != nil {
		return "", err
	}
	return "---\n" + strings.TrimSpace(string(raw)) + "\n---\n\n" + strings.TrimSpace(record.Body) + "\n", nil
}

func splitFrontmatter(content string) (string, string) {
	content = strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(content, "---") {
		return "", content
	}
	rest := strings.TrimPrefix(content, "---")
	rest = strings.TrimPrefix(rest, "\r\n")
	rest = strings.TrimPrefix(rest, "\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return "", content
	}
	meta := rest[:idx]
	body := rest[idx+len("\n---"):]
	body = strings.TrimPrefix(body, "\r\n")
	body = strings.TrimPrefix(body, "\n")
	return meta, body
}

func normalizeType(t Type) Type {
	switch t {
	case TypeUser, TypeFeedback, TypeProject, TypeReference:
		return t
	default:
		return TypeUser
	}
}

func isValidType(t Type) bool {
	switch t {
	case TypeUser, TypeFeedback, TypeProject, TypeReference:
		return true
	default:
		return false
	}
}

var temporaryMemoryMarkers = []string{
	"this session", "current session", "this turn", "current turn",
	"this task", "current task", "for now", "just this time", "today only",
	"本次会话", "当前会话", "这一轮", "当前轮次", "本次任务", "当前任务", "暂时",
	"今回だけ", "このセッション", "現在のタスク",
}

func shouldStoreMemory(candidate Record, existing []Record) bool {
	if candidate.Scope != ScopePersistent || !isValidType(candidate.Type) {
		return false
	}
	if strings.TrimSpace(candidate.Name) == "" || strings.TrimSpace(candidate.Description) == "" || strings.TrimSpace(candidate.Body) == "" {
		return false
	}

	candidateText := normalizedMemoryText(candidate.Name + "\n" + candidate.Description + "\n" + candidate.Body)
	for _, marker := range temporaryMemoryMarkers {
		if strings.Contains(candidateText, normalizedMemoryText(marker)) {
			return false
		}
	}

	candidateSlug := slugify(candidate.Name)
	candidateDescription := normalizedMemoryText(candidate.Description)
	candidateBody := normalizedMemoryText(candidate.Body)
	for _, record := range existing {
		if slugify(record.Name) == candidateSlug ||
			normalizedMemoryText(record.Description) == candidateDescription ||
			normalizedMemoryText(record.Body) == candidateBody {
			return false
		}
	}
	return true
}

func normalizedMemoryText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

var slugPart = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(name string) string {
	slug := strings.ToLower(strings.TrimSpace(name))
	slug = slugPart.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "memory"
	}
	return slug
}

func toCatalog(records []Record) []CatalogItem {
	catalog := make([]CatalogItem, 0, len(records))
	for i, record := range records {
		catalog = append(catalog, CatalogItem{
			Index:       i,
			Filename:    record.Filename,
			Name:        record.Name,
			Description: record.Description,
			Type:        record.Type,
		})
	}
	return catalog
}

func fallbackSelect(recent string, catalog []CatalogItem, maxItems int) []int {
	words := keywordSet(recent)
	var selected []int
	for _, item := range catalog {
		text := strings.ToLower(item.Name + " " + item.Description)
		for word := range words {
			if strings.Contains(text, word) {
				selected = append(selected, item.Index)
				break
			}
		}
		if len(selected) >= maxItems {
			break
		}
	}
	return selected
}

func keywordSet(text string) map[string]bool {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	out := map[string]bool{}
	for _, word := range words {
		if len(word) > 3 {
			out[word] = true
		}
	}
	return out
}

func messageText(message Message) string {
	if strings.TrimSpace(message.Content) != "" {
		return message.Content
	}
	parts := make([]string, 0, len(message.Blocks))
	for _, block := range message.Blocks {
		switch block.Type {
		case BlockText, BlockToolResult:
			if strings.TrimSpace(block.Text) != "" {
				parts = append(parts, block.Text)
			}
		case BlockToolUse:
			if block.ToolName != "" {
				parts = append(parts, "[tool_use "+block.ToolName+"]")
			}
		}
	}
	return strings.Join(parts, "\n")
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if len(line) > 80 {
				return line[:80]
			}
			return line
		}
	}
	return ""
}

func fallback(value, fallbackValue string) string {
	if strings.TrimSpace(value) == "" {
		return fallbackValue
	}
	return value
}

func reverse(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}
