package memory

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"go-agent-harness/internal/protocol"
)

// s09 Recall：先选择目录索引，再按总量预算加载正文；选择失败时回退关键词匹配。
// 根据最近对话内容，调用LLM获取与对话可能相关的memory内容
func (m *Manager) LoadRelevant(ctx context.Context, messages []protocol.Message, selectRelevant Selector) (string, error) {
	records, err := m.List()
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}

	recent := RecentUserText(messages, 3, 4000)
	if strings.TrimSpace(recent) == "" {
		return "", nil
	}

	catalog := toCatalog(records)
	indices, err := selectRelevant(ctx, recent, catalog, m.cfg.MaxRelevant)
	if err != nil {
		indices = fallbackSelect(recent, catalog, m.cfg.MaxRelevant)
	}
	if len(indices) == 0 {
		return "", nil
	}

	type recalledMemory struct {
		Source  string `json:"source"`
		Content string `json:"content"`
	}
	loaded := make([]recalledMemory, 0, len(indices))
	remaining := m.cfg.RecallCharLimit
	seen := map[int]bool{}
	for _, idx := range indices {
		if idx < 0 || idx >= len(records) || seen[idx] || remaining <= 0 {
			continue
		}
		seen[idx] = true
		content, err := m.readContent(records[idx].Filename)
		if err != nil {
			return "", err
		}
		if len(content) > remaining {
			content = content[:remaining]
		}
		if strings.TrimSpace(content) != "" {
			loaded = append(loaded, recalledMemory{Source: records[idx].Filename, Content: content})
			remaining -= len(content)
		}
		if len(seen) >= m.cfg.MaxRelevant {
			break
		}
	}
	if len(loaded) == 0 {
		return "", nil
	}
	raw, err := json.MarshalIndent(loaded, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
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
	type rankedItem struct {
		index    int
		filename string
		score    int
	}
	ranked := make([]rankedItem, 0, len(catalog))
	for _, item := range catalog {
		text := strings.ToLower(item.Name + " " + item.Description)
		score := 0
		for word := range words {
			if strings.Contains(text, word) {
				score++
			}
		}
		if score > 0 {
			ranked = append(ranked, rankedItem{index: item.Index, filename: item.Filename, score: score})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].filename < ranked[j].filename
	})
	selected := make([]int, 0, min(maxItems, len(ranked)))
	for _, item := range ranked {
		selected = append(selected, item.index)
		if len(selected) >= maxItems {
			break
		}
	}
	return selected
}

var memoryKeywordRE = regexp.MustCompile(`(?i)[a-z0-9_]{3,}|[\p{Han}]{2,}`)

func keywordSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, word := range memoryKeywordRE.FindAllString(strings.ToLower(text), -1) {
		out[word] = true
	}
	return out
}
