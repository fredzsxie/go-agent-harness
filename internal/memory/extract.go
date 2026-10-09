package memory

import (
	"context"
	"strings"

	"go-agent-harness/internal/protocol"
)

// s09 Extraction：模型只产生候选记录；持久作用域、临时措辞和重复检查决定是否落盘。
// 抽取最近 12 条记录（不超过 8000 个字符），调用 LLM 判断是否有需要提取为 memory 的内容。
func (m *Manager) Extract(ctx context.Context, messages []protocol.Message, extract Extractor) (int, error) {
	dialogue := FormatRecentMessages(messages, 12, 8000)
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
