package subagent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"go-agent-harness/config"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/logging"
	llmmodel "go-agent-harness/internal/model"
	"go-agent-harness/internal/prompt"
	"go-agent-harness/loop"
)

const maxTurns = 30

type Manager struct {
	worker *loop.Worker
}

func New(cfg config.LLMConfig, registry *loop.Registry, hookManager *hooks.Manager, out io.Writer) *Manager {
	if hookManager == nil {
		hookManager = hooks.NewManager()
	}
	return &Manager{worker: loop.NewWorker(llmmodel.NewAnthropic(cfg), registry, hookManager)}
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
		turn, err := m.worker.RunTurn(ctx, prompt.Subagent(), messages, nil)
		if err != nil {
			return "", err
		}

		assistantMessage := turn.Assistant
		messages = append(messages, assistantMessage)

		if !turn.HasTools {
			finished = true
			break
		}

		toolResults := turn.Tools.Results
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

func (m *Manager) logf(format string, args ...any) {
	logging.Printf(format, args...)
}
