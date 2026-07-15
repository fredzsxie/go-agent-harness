// Package compact implements the s08 context compaction pipeline.
//
// The package is intentionally independent from the Anthropic SDK and the loop
// package. Callers provide a summarizer callback when LLM-backed compaction is
// needed.
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

	"go-agent-harness/internal/conversation"
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

type Summarizer func(ctx context.Context, messages []Message) (string, error)

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

func (m *Manager) Prepare(ctx context.Context, messages []Message, summarize Summarizer) ([]Message, bool, error) {
	fmt.Printf("[Prepare] L3 compact\n")
	// fmt.Printf("[Prepare] before L3 compact: %+v\n", messages)
	prepared, err := m.ToolResultBudget(messages)
	if err != nil {
		return messages, false, err
	}

	fmt.Printf("[Prepare] L1 compact\n")
	// fmt.Printf("[Prepare] before L1 compact: %+v\n", prepared)
	prepared = m.SnipCompact(prepared)

	fmt.Printf("[Prepare] L2 compact\n")
	// fmt.Printf("[Prepare] before L2 compact: %+v\n", prepared)
	prepared = m.MicroCompact(prepared)
	// fmt.Printf("[Prepare] after L2 compact: %+v\n", prepared)
	if EstimateSize(prepared) <= m.cfg.ContextLimit {
		return prepared, false, nil
	}

	// 如果经过三层压缩后，上下文仍旧超过长度，则调用LLM来进一步压缩
	fmt.Printf("[Prepare] L4 compact, content: %v\n", prepared)
	compacted, err := m.CompactHistory(ctx, prepared, summarize)
	if err != nil {
		return prepared, false, err
	}
	return compacted, true, nil
}

func EstimateSize(messages []Message) int {
	raw, err := json.Marshal(messages)
	if err != nil {
		return len(fmt.Sprint(messages))
	}
	return len(raw)
}

// L1: snip_compact 裁切无关旧会话
func (m *Manager) SnipCompact(messages []Message) []Message {
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
	out := make([]Message, 0, headEnd+1+len(messages)-tailStart)
	out = append(out, messages[:headEnd]...)
	out = append(out, Message{
		Role:    RoleUser,
		Content: fmt.Sprintf("[snipped %d messages from conversation middle]", snipped),
	})
	out = append(out, messages[tailStart:]...)

	// fmt.Printf("[Context Compact - L1]: result: %+v\n\n", out)
	return out
}

// L2: micro_compact 旧工具调用结果替换为占位符
func (m *Manager) MicroCompact(messages []Message) []Message {
	positions := collectToolResults(messages)
	if len(positions) <= m.cfg.KeepRecentToolResults {
		return messages
	}

	out := cloneMessages(messages)
	for _, pos := range positions[:len(positions)-m.cfg.KeepRecentToolResults] {
		block := &out[pos.messageIndex].Blocks[pos.blockIndex]
		if len(block.Text) > 120 {
			block.Text = "[Earlier tool result compacted. Re-run if needed.]"
		}
	}

	// fmt.Printf("[Context Compact - L2]: result: %+v\n\n", out)
	return out
}

// L3: tool_resutl_budget 大结果落盘
func (m *Manager) ToolResultBudget(messages []Message) ([]Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}

	last := messages[len(messages)-1]
	if last.Role != RoleUser || len(last.Blocks) == 0 {
		return messages, nil
	}

	type rankedBlock struct {
		index int
		size  int
	}
	blocks := make([]rankedBlock, 0, len(last.Blocks))
	total := 0
	for i, block := range last.Blocks {
		if block.Type != BlockToolResult {
			continue
		}
		size := len(block.Text)
		total += size
		blocks = append(blocks, rankedBlock{index: i, size: size})
	}
	if total <= m.cfg.ToolResultBudget {
		return messages, nil
	}

	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].size > blocks[j].size
	})

	out := cloneMessages(messages)
	lastBlocks := out[len(out)-1].Blocks
	for _, ranked := range blocks {
		if total <= m.cfg.ToolResultBudget {
			break
		}
		block := &lastBlocks[ranked.index]
		if len(block.Text) <= m.cfg.PersistThreshold {
			continue
		}

		original := block.Text
		persisted, err := m.persistLargeOutput(block.ToolUseID, original)
		if err != nil {
			return messages, err
		}
		block.Text = persisted
		total += len(persisted) - len(original)
	}

	// fmt.Printf("[Context Compact - L3]: compact result: %+v\n\n", out)
	return out, nil
}

// L4: compact_history - 调用LLM生成全量摘要
func (m *Manager) CompactHistory(ctx context.Context, messages []Message, summarize Summarizer) ([]Message, error) {
	return m.compactWithPrefix(ctx, messages, summarize, "[Compacted]")
}

// 应急兜底 reactive_compact (api返回413 / prompt too long -> 字节级裁剪)
func (m *Manager) ReactiveCompact(ctx context.Context, messages []Message, summarize Summarizer) ([]Message, error) {
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

	out := make([]Message, 0, 1+len(messages)-tailStart)
	out = append(out, Message{Role: RoleUser, Content: "[Reactive compact]\n\n" + summary})
	out = append(out, messages[tailStart:]...)

	// fmt.Printf("[Context Compact - Reactive Compact]: result: %+v\n\n", out)
	return out, nil
}

func (m *Manager) WriteTranscript(messages []Message) (string, error) {
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

func (m *Manager) compactWithPrefix(ctx context.Context, messages []Message, summarize Summarizer, prefix string) ([]Message, error) {
	if _, err := m.WriteTranscript(messages); err != nil {
		return nil, err
	}
	summary, err := summarizeHistory(ctx, messages, summarize)
	if err != nil {
		return nil, err
	}

	// fmt.Printf("[Context Compact - L4]: compacted summary: %+v\n\n", summary)
	return []Message{{Role: RoleUser, Content: prefix + "\n\n" + summary}}, nil
}

func summarizeHistory(ctx context.Context, messages []Message, summarize Summarizer) (string, error) {
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
	if len(output) <= m.cfg.PersistThreshold {
		return output, nil
	}
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

	preview := output
	if len(preview) > m.cfg.PreviewBytes {
		preview = preview[:m.cfg.PreviewBytes]
	}
	return fmt.Sprintf("<persisted-output>\nFull output: %s\nPreview:\n%s\n</persisted-output>", path, preview), nil
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

func collectToolResults(messages []Message) []toolResultPosition {
	positions := make([]toolResultPosition, 0)
	for mi, message := range messages {
		if message.Role != RoleUser {
			continue
		}
		for bi, block := range message.Blocks {
			if block.Type == BlockToolResult {
				positions = append(positions, toolResultPosition{messageIndex: mi, blockIndex: bi})
			}
		}
	}
	return positions
}

func messageHasToolUse(message Message) bool {
	if message.Role != RoleAssistant {
		return false
	}
	for _, block := range message.Blocks {
		if block.Type == BlockToolUse {
			return true
		}
	}
	return false
}

func isToolResultMessage(message Message) bool {
	if message.Role != RoleUser {
		return false
	}
	for _, block := range message.Blocks {
		if block.Type == BlockToolResult {
			return true
		}
	}
	return false
}

func cloneMessages(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	for _, message := range messages {
		copyMessage := Message{
			Role:    message.Role,
			Content: message.Content,
		}
		if len(message.Blocks) > 0 {
			copyMessage.Blocks = make([]ContentBlock, 0, len(message.Blocks))
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
