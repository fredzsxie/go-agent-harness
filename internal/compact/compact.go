// Package compact 实现 s08 上下文压缩流水线。
// 本包不依赖 Anthropic SDK；需要 LLM 摘要时由调用方传入 Summarizer。
package compact

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/protocol"
)

const (
	DefaultContextLimit          = 50000
	DefaultKeepRecentToolResults = 3
	DefaultMaxMessages           = 50
	DefaultPersistThreshold      = 30000
	DefaultToolResultBudget      = 200000
	DefaultPreviewBytes          = 2000
	DefaultMaxReactiveRetries    = 1
)

type Summarizer func(ctx context.Context, messages []protocol.Message) (string, error)

type Config struct {
	WorkDir               string
	ContextLimit          int
	KeepRecentToolResults int
	MaxMessages           int
	PersistThreshold      int
	ToolResultBudget      int
	PreviewBytes          int
	MaxReactiveRetries    int
	TranscriptDir         string
	ToolResultsDir        string
}

type Manager struct {
	cfg Config
}

func New(cfg Config) *Manager {
	if cfg.WorkDir == "" {
		cfg.WorkDir = "."
	}
	if cfg.ContextLimit <= 0 {
		cfg.ContextLimit = DefaultContextLimit
	}
	if cfg.KeepRecentToolResults <= 0 {
		cfg.KeepRecentToolResults = DefaultKeepRecentToolResults
	}
	if cfg.MaxMessages <= 0 {
		cfg.MaxMessages = DefaultMaxMessages
	}
	if cfg.PersistThreshold <= 0 {
		cfg.PersistThreshold = DefaultPersistThreshold
	}
	if cfg.ToolResultBudget <= 0 {
		cfg.ToolResultBudget = DefaultToolResultBudget
	}
	if cfg.PreviewBytes <= 0 {
		cfg.PreviewBytes = DefaultPreviewBytes
	}
	if cfg.MaxReactiveRetries <= 0 {
		cfg.MaxReactiveRetries = DefaultMaxReactiveRetries
	}
	if cfg.TranscriptDir == "" {
		cfg.TranscriptDir = filepath.Join(cfg.WorkDir, ".transcripts")
	}
	if cfg.ToolResultsDir == "" {
		cfg.ToolResultsDir = filepath.Join(cfg.WorkDir, ".task_outputs", "tool-results")
	}
	return &Manager{cfg: cfg}
}

func (m *Manager) MaxReactiveRetries() int {
	return m.cfg.MaxReactiveRetries
}

func (m *Manager) Prepare(ctx context.Context, messages []protocol.Message, summarize Summarizer) ([]protocol.Message, bool, error) {
	logger.Info("[Prepare] L3 compact")
	prepared, err := m.ToolResultBudget(messages)
	if err != nil {
		return messages, false, err
	}
	if EstimateSize(prepared) <= m.cfg.ContextLimit {
		return prepared, false, nil
	}

	logger.Info("[Prepare] L1 compact")
	prepared = m.SnipCompact(prepared)
	if EstimateSize(prepared) <= m.cfg.ContextLimit {
		return prepared, false, nil
	}

	logger.Info("[Prepare] L2 compact")
	prepared = m.MicroCompact(prepared)
	if EstimateSize(prepared) <= m.cfg.ContextLimit {
		return prepared, false, nil
	}

	// 如果经过三层压缩后，上下文仍旧超过长度，则调用LLM来进一步压缩
	logger.Info("[Prepare] L4 compact, content: %v", prepared)
	compacted, err := m.CompactHistory(ctx, prepared, summarize)
	if err != nil {
		return prepared, false, err
	}
	return compacted, true, nil
}

func EstimateSize(messages []protocol.Message) int {
	raw, err := json.Marshal(messages)
	if err != nil {
		return len(fmt.Sprint(messages))
	}
	return len(raw)
}

// L1: snip_compact 裁切无关旧会话
func (m *Manager) SnipCompact(messages []protocol.Message) []protocol.Message {
	if len(messages) <= m.cfg.MaxMessages {
		return messages
	}

	keepHead := 3
	if keepHead > len(messages) {
		keepHead = len(messages)
	}
	keepTail := m.cfg.MaxMessages - keepHead
	if keepTail < 0 {
		keepTail = 0
	}
	headEnd := keepHead
	tailStart := len(messages) - keepTail
	if tailStart < headEnd {
		tailStart = headEnd
	}

	// 处理切口边界条件，不能把 assistant(tool_use) 和后面的 user(tool_result) 拆开
	if headEnd > 0 && messageHasToolUse(messages[headEnd-1]) {
		for headEnd < len(messages) && isToolResultMessage(messages[headEnd]) {
			headEnd++
		}
	}
	if tailStart > 0 && tailStart < len(messages) && isToolResultMessage(messages[tailStart]) && messageHasToolUse(messages[tailStart-1]) {
		tailStart--
	}
	if headEnd >= tailStart {
		return messages
	}

	// 拼接压缩后的对话
	snipped := tailStart - headEnd
	out := make([]protocol.Message, 0, headEnd+1+len(messages)-tailStart)
	out = append(out, messages[:headEnd]...)
	out = append(out, protocol.Message{
		Role:    protocol.RoleUser,
		Content: fmt.Sprintf("[snipped %d messages from conversation middle]", snipped),
	})
	out = append(out, messages[tailStart:]...)

	return out
}

// L2: micro_compact 旧工具调用结果替换为占位符
func (m *Manager) MicroCompact(messages []protocol.Message) []protocol.Message {
	positions := collectToolResults(messages)
	lastAssistant := -1
	for i, message := range messages {
		if message.Role == protocol.RoleAssistant {
			lastAssistant = i
		}
	}

	consumed := make([]toolResultPosition, 0, len(positions))
	for _, pos := range positions {
		// 最新 assistant 消息后的结果尚未被模型消费，即使超过常规数量也必须保留整个批次。
		if lastAssistant >= 0 && pos.messageIndex > lastAssistant {
			continue
		}
		consumed = append(consumed, pos)
	}
	if len(consumed) <= m.cfg.KeepRecentToolResults {
		return messages
	}

	out := cloneMessages(messages)
	for _, pos := range consumed[:len(consumed)-m.cfg.KeepRecentToolResults] {
		block := &out[pos.messageIndex].Blocks[pos.blockIndex]
		if len(block.Text) > 120 {
			path, err := m.saveOutput(block.ToolUseID, block.Text)
			if err != nil {
				block.Text = "[Earlier tool result compacted. Re-run if needed.]"
				continue
			}
			block.Text = fmt.Sprintf("[Earlier tool result saved at %s]", path)
		}
	}

	return out
}

// L3: tool_resutl_budget 大结果落盘
func (m *Manager) ToolResultBudget(messages []protocol.Message) ([]protocol.Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	type rankedBlock struct {
		position toolResultPosition
		size     int
	}
	lastAssistant := -1
	for i, message := range messages {
		if message.Role == protocol.RoleAssistant {
			lastAssistant = i
		}
	}
	positions := collectToolResults(messages)
	blocks := make([]rankedBlock, 0, len(positions))
	total := 0
	for _, position := range positions {
		if lastAssistant >= 0 && position.messageIndex <= lastAssistant {
			continue
		}
		block := messages[position.messageIndex].Blocks[position.blockIndex]
		size := len(block.Text)
		total += size
		blocks = append(blocks, rankedBlock{position: position, size: size})
	}
	if len(blocks) == 0 {
		return messages, nil
	}
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].size > blocks[j].size
	})

	out := cloneMessages(messages)
	changed := false
	for _, ranked := range blocks {
		if ranked.size <= m.cfg.PersistThreshold && total <= m.cfg.ToolResultBudget {
			break
		}
		block := &out[ranked.position.messageIndex].Blocks[ranked.position.blockIndex]

		original := block.Text
		persisted, err := m.persistLargeOutput(block.ToolUseID, original)
		if err != nil {
			return messages, err
		}
		block.Text = persisted
		total += len(persisted) - len(original)
		changed = true
	}

	if !changed {
		return messages, nil
	}
	return out, nil
}

// L4: compact_history - 调用LLM生成全量摘要
func (m *Manager) CompactHistory(ctx context.Context, messages []protocol.Message, summarize Summarizer) ([]protocol.Message, error) {
	return m.compactWithPrefix(ctx, messages, summarize, "[Compacted]")
}

// 应急兜底 reactive_compact (api返回413 / prompt too long -> 字节级裁剪)
func (m *Manager) ReactiveCompact(ctx context.Context, messages []protocol.Message, summarize Summarizer) ([]protocol.Message, error) {
	if _, err := m.WriteTranscript(messages); err != nil {
		return nil, err
	}
	summary, err := summarizeHistory(ctx, messages, summarize)
	if err != nil {
		return nil, err
	}

	tailStart := len(messages) - 5
	if tailStart < 0 {
		tailStart = 0
	}
	if tailStart > 0 && tailStart < len(messages) && isToolResultMessage(messages[tailStart]) && messageHasToolUse(messages[tailStart-1]) {
		tailStart--
	}

	out := make([]protocol.Message, 0, 1+len(messages)-tailStart)
	out = append(out, protocol.Message{Role: protocol.RoleUser, Content: "[Reactive compact]\n\n" + summary})
	out = append(out, messages[tailStart:]...)

	return out, nil
}

func (m *Manager) WriteTranscript(messages []protocol.Message) (string, error) {
	if err := os.MkdirAll(m.cfg.TranscriptDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(m.cfg.TranscriptDir, fmt.Sprintf("transcript_%d.jsonl", time.Now().UnixNano()))
	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	for _, message := range messages {
		if err := encoder.Encode(message); err != nil {
			return "", err
		}
	}
	return path, nil
}

func (m *Manager) compactWithPrefix(ctx context.Context, messages []protocol.Message, summarize Summarizer, prefix string) ([]protocol.Message, error) {
	if _, err := m.WriteTranscript(messages); err != nil {
		return nil, err
	}
	summary, err := summarizeHistory(ctx, messages, summarize)
	if err != nil {
		return nil, err
	}

	return []protocol.Message{{Role: protocol.RoleUser, Content: prefix + "\n\n" + summary}}, nil
}

func summarizeHistory(ctx context.Context, messages []protocol.Message, summarize Summarizer) (string, error) {
	if summarize == nil {
		return "", fmt.Errorf("compact summarizer is nil")
	}
	summary, err := summarize(ctx, messages)
	if err != nil {
		return "", err
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return "(empty summary)", nil
	}
	return summary, nil
}

func (m *Manager) persistLargeOutput(toolUseID string, output string) (string, error) {
	path, err := m.saveOutput(toolUseID, output)
	if err != nil {
		return "", err
	}

	preview := output
	if len(preview) > m.cfg.PreviewBytes {
		preview = preview[:m.cfg.PreviewBytes]
	}
	return fmt.Sprintf("<persisted-output>\nFull output: %s\nPreview:\n%s\n</persisted-output>", path, preview), nil
}

func (m *Manager) saveOutput(toolUseID string, output string) (string, error) {
	if err := os.MkdirAll(m.cfg.ToolResultsDir, 0o755); err != nil {
		return "", err
	}
	name := safeOutputName(toolUseID, output)
	path := filepath.Join(m.cfg.ToolResultsDir, name+".txt")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	return path, nil
}

func safeOutputName(toolUseID string, output string) string {
	base := strings.TrimSpace(toolUseID)
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '-'
		}
	}, base)
	base = strings.Trim(base, "-")
	if base == "" {
		sum := sha1.Sum([]byte(output))
		base = "unknown-" + hex.EncodeToString(sum[:])[:12]
	}
	return base
}

type toolResultPosition struct {
	messageIndex int
	blockIndex   int
}

func collectToolResults(messages []protocol.Message) []toolResultPosition {
	positions := make([]toolResultPosition, 0)
	for mi, message := range messages {
		if message.Role != protocol.RoleUser {
			continue
		}
		for bi, block := range message.Blocks {
			if block.Type == protocol.BlockToolResult {
				positions = append(positions, toolResultPosition{messageIndex: mi, blockIndex: bi})
			}
		}
	}
	return positions
}

func messageHasToolUse(message protocol.Message) bool {
	if message.Role != protocol.RoleAssistant {
		return false
	}
	for _, block := range message.Blocks {
		if block.Type == protocol.BlockToolUse {
			return true
		}
	}
	return false
}

func isToolResultMessage(message protocol.Message) bool {
	if message.Role != protocol.RoleUser {
		return false
	}
	for _, block := range message.Blocks {
		if block.Type == protocol.BlockToolResult {
			return true
		}
	}
	return false
}

func cloneMessages(messages []protocol.Message) []protocol.Message {
	out := make([]protocol.Message, 0, len(messages))
	for _, message := range messages {
		copyMessage := protocol.Message{
			Role:    message.Role,
			Content: message.Content,
		}
		if len(message.Blocks) > 0 {
			copyMessage.Blocks = make([]protocol.ContentBlock, 0, len(message.Blocks))
			for _, block := range message.Blocks {
				copyBlock := block
				if block.Input != nil {
					copyBlock.Input = make(map[string]any, len(block.Input))
					for key, value := range block.Input {
						copyBlock.Input[key] = value
					}
				}
				copyMessage.Blocks = append(copyMessage.Blocks, copyBlock)
			}
		}
		out = append(out, copyMessage)
	}
	return out
}
