package subagent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"go-agent-harness/config"
	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/hooks"
	llmmodel "go-agent-harness/internal/model"
	"go-agent-harness/internal/prompt"
	"go-agent-harness/loop"
)

const maxTurns = 30

type Manager struct {
	client   agent.Model
	registry *loop.Registry
	hooks    *hooks.Manager
	out      io.Writer
}

func New(cfg config.LLMConfig, registry *loop.Registry, hookManager *hooks.Manager, out io.Writer) *Manager {
	if hookManager == nil {
		hookManager = hooks.NewManager()
	}
	return &Manager{
		client:   llmmodel.NewAnthropic(cfg),
		registry: registry,
		hooks:    hookManager,
		out:      out,
	}
}

func (m *Manager) RunTask(ctx context.Context, input any) (string, error) {
	payload, ok := input.(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid task payload")
	}

	description, _ := payload["description"].(string)
	description = strings.TrimSpace(description)
	if description == "" {
		return "", fmt.Errorf("missing description")
	}

	return m.spawn(ctx, description)
}

func (m *Manager) spawn(ctx context.Context, description string) (string, error) {
	m.logf("\n[Subagent spawned]\n")

	messages := []loop.Message{{Role: loop.RoleUser, Content: description}}
	finished := false

	for range maxTurns {
		resp, err := m.client.Complete(ctx, agent.ModelRequest{
			MaxTokens: 8000,
			Messages:  messages,
			Tools:     m.registry.Specs(),
			System:    prompt.Subagent(),
		})
		if err != nil {
			return "", err
		}

		assistantMessage := resp.Message
		messages = append(messages, assistantMessage)

		if !hasToolUse(assistantMessage) {
			finished = true
			break
		}

		toolResults, _ := loop.ExecuteToolUses(ctx, assistantMessage.Blocks, m.registry, m.hooks, func(call hooks.ToolCall, result string, _ bool) {
			// m.logf("  [sub] %s: %s\n", call.Name, preview(result, 100))
		})
		if len(toolResults) == 0 {
			return "", fmt.Errorf("subagent requested tool_use without tool blocks")
		}

		messages = append(messages, loop.Message{
			Role:   loop.RoleUser,
			Blocks: toolResults,
		})
	}

	result := loop.LatestAssistantText(messages)
	if strings.TrimSpace(result) == "" {
		if finished {
			result = "(no summary)"
		} else {
			result = "Subagent stopped after 30 turns without final answer."
		}
	}

	m.logf("[Subagent done]\n\n")
	return strings.TrimSpace(result), nil
}

func hasToolUse(message loop.Message) bool {
	for _, block := range message.Blocks {
		if block.Type == loop.BlockToolUse {
			return true
		}
	}
	return false
}

func preview(text string, max int) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

func (m *Manager) logf(format string, args ...any) {
	if m.out == nil {
		return
	}
	_, _ = fmt.Fprintf(m.out, format, args...)
}
