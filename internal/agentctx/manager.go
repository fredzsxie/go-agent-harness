// Package agentctx 统一编排 Prompt、Compact 与 Memory 的会话上下文生命周期。
package agentctx

import (
	"context"

	"go-agent-harness/internal/compact"
	"go-agent-harness/internal/llm"
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
	services      *modelServices
	compact       *compact.Manager
	memory        *memory.Manager
	promptBuilder *prompt.Builder
	systemPrompt  string
	memorySection string
}

// Config 明确上下文的存储范围与模型依赖；每个 Session 应独享一个 Manager。
// Compact、Memory 可由装配层提供，以便调整预算或使用临时目录测试。
type Config struct {
	WorkDir       string
	Model         llm.Model
	PromptBuilder *prompt.Builder
	SystemPrompt  string
	Compact       *compact.Manager
	Memory        *memory.Manager
}

func New(cfg Config) *Manager {
	if cfg.Compact == nil {
		cfg.Compact = compact.New(compact.Config{WorkDir: cfg.WorkDir})
	}
	if cfg.Memory == nil {
		cfg.Memory = memory.New(memory.Config{WorkDir: cfg.WorkDir})
	}
	return &Manager{
		compact: cfg.Compact, memory: cfg.Memory, promptBuilder: cfg.PromptBuilder,
		systemPrompt: cfg.SystemPrompt, services: &modelServices{model: cfg.Model},
	}
}

// StartRequest 在一次用户请求开始时召回 Memory，并生成初始 System Prompt。
func (m *Manager) StartRequest(ctx context.Context, messages []protocol.Message, toolNames []string, live prompt.LiveContext) (string, error) {
	relevant, err := m.memory.LoadRelevant(ctx, messages, m.services.selectRelevantMemories)
	if err != nil {
		return "", err
	}
	section, err := m.memory.SystemSection(relevant)
	if err != nil {
		return "", err
	}
	if m.promptBuilder == nil {
		m.memorySection = section
		return prompt.Build(m.systemPrompt, section, "When the user says \"remember\" or expresses a stable preference, save it as memory after the turn."), nil
	}
	m.memorySection = section
	return m.promptBuilder.Get(prompt.Context{EnabledTools: toolNames, Memories: section, Live: live}), nil
}

// RefreshPrompt 复用已召回的 Memory，并刷新工具与实时运行状态。
func (m *Manager) RefreshPrompt(toolNames []string, live prompt.LiveContext) string {
	if m.promptBuilder == nil {
		return prompt.Build(m.systemPrompt, m.memorySection, "When the user says \"remember\" or expresses a stable preference, save it as memory after the turn.")
	}
	return m.promptBuilder.Get(prompt.Context{EnabledTools: toolNames, Memories: m.memorySection, Live: live})
}

func (m *Manager) Prepare(ctx context.Context, messages []protocol.Message, activeRequest string) ([]protocol.Message, error) {
	prepared, _, err := m.compact.Prepare(ctx, messages, activeRequest, m.services.summarizeCompactHistory)
	return prepared, err
}

func (m *Manager) ReactiveCompact(ctx context.Context, messages []protocol.Message, activeRequest string) ([]protocol.Message, error) {
	return m.compact.ReactiveCompact(ctx, messages, activeRequest, m.services.summarizeCompactHistory)
}

func (m *Manager) Compact(ctx context.Context, messages []protocol.Message, activeRequest string) ([]protocol.Message, error) {
	return m.compact.CompactHistory(ctx, messages, activeRequest, m.services.summarizeCompactHistory)
}

func (m *Manager) MaxReactiveRetries() int {
	return m.compact.MaxReactiveRetries()
}

// Finalize 在会话正常结束后提取 Memory；只有确实新增内容时才执行合并。
func (m *Manager) Finalize(ctx context.Context, messages []protocol.Message) MemoryReport {
	report := MemoryReport{}
	report.Extracted, report.ExtractError = m.memory.Extract(ctx, messages, m.services.extractMemories)
	if report.ExtractError != nil || report.Extracted == 0 {
		return report
	}
	report.Before, report.After, report.ConsolidateErr = m.memory.Consolidate(ctx, m.services.consolidateMemories)
	return report
}
