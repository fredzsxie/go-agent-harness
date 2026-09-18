// Package agentctx 统一编排 Prompt、Compact 与 Memory 的会话上下文生命周期。
package agentctx

import (
	"context"

	"go-agent-harness/internal/compact"
	"go-agent-harness/internal/memory"
	"go-agent-harness/internal/prompt"
	"go-agent-harness/internal/protocol"
)

type MemoryReport struct {
	Extracted      int
	ExtractError   error
	Before         int
	After          int
	ConsolidateErr error
}

type Manager struct {
	compact       *compact.Manager
	memory        *memory.Manager
	promptBuilder *prompt.Builder
	legacyPrompt  string
	memorySection string
}

func New(builder *prompt.Builder, legacyPrompt string) *Manager {
	return &Manager{
		compact:       compact.New(compact.Config{}),
		memory:        memory.New(memory.Config{}),
		promptBuilder: builder,
		legacyPrompt:  legacyPrompt,
	}
}

// StartRequest 在一次用户请求开始时召回 Memory，并生成本次请求的初始 System Prompt。
func (m *Manager) StartRequest(ctx context.Context, messages []protocol.Message, toolNames []string, selector memory.Selector) (string, error) {
	relevant, err := m.memory.LoadRelevant(ctx, messages, selector)
	if err != nil {
		return "", err
	}
	section, err := m.memory.SystemSection(relevant)
	if err != nil {
		return "", err
	}
	if m.promptBuilder == nil {
		m.memorySection = section
		return prompt.Build(m.legacyPrompt, section, "When the user says \"remember\" or expresses a stable preference, save it as memory after the turn."), nil
	}
	m.memorySection = section
	return m.promptBuilder.Get(prompt.Context{EnabledTools: toolNames, Memories: section}), nil
}

// RefreshPrompt 复用已召回的 Memory，并刷新动态工具。
func (m *Manager) RefreshPrompt(toolNames []string) string {
	if m.promptBuilder == nil {
		return prompt.Build(m.legacyPrompt, m.memorySection, "When the user says \"remember\" or expresses a stable preference, save it as memory after the turn.")
	}
	return m.promptBuilder.Get(prompt.Context{EnabledTools: toolNames, Memories: m.memorySection})
}

func (m *Manager) Prepare(ctx context.Context, messages []protocol.Message, activeRequest string, summarize compact.Summarizer) ([]protocol.Message, error) {
	prepared, _, err := m.compact.Prepare(ctx, messages, activeRequest, summarize)
	return prepared, err
}

func (m *Manager) ReactiveCompact(ctx context.Context, messages []protocol.Message, activeRequest string, summarize compact.Summarizer) ([]protocol.Message, error) {
	return m.compact.ReactiveCompact(ctx, messages, activeRequest, summarize)
}

func (m *Manager) Compact(ctx context.Context, messages []protocol.Message, activeRequest string, summarize compact.Summarizer) ([]protocol.Message, error) {
	return m.compact.CompactHistory(ctx, messages, activeRequest, summarize)
}

func (m *Manager) MaxReactiveRetries() int {
	return m.compact.MaxReactiveRetries()
}

// Finalize 在会话正常结束后提取 Memory；只有确实新增内容时才执行合并。
func (m *Manager) Finalize(ctx context.Context, messages []protocol.Message, extract memory.Extractor, consolidate memory.Consolidator) MemoryReport {
	report := MemoryReport{}
	report.Extracted, report.ExtractError = m.memory.Extract(ctx, messages, extract)
	if report.ExtractError != nil || report.Extracted == 0 {
		return report
	}
	report.Before, report.After, report.ConsolidateErr = m.memory.Consolidate(ctx, consolidate)
	return report
}
