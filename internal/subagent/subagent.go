package subagent

import (
	"context"
	"fmt"
	"strings"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/prompt"
)

const maxTurns = 30

type Manager struct {
	worker *agent.Worker
}

func New(model agent.Model, registry *agent.Registry, hookManager *hooks.Manager) *Manager {
	if hookManager == nil {
		hookManager = hooks.NewManager()
	}
	return &Manager{worker: agent.NewWorker(model, registry, hookManager)}
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
	logger.Info("[Subagent] spawned")

	messages := []agent.Message{{Role: agent.RoleUser, Content: description}}
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

		messages = append(messages, agent.Message{
			Role:   agent.RoleUser,
			Blocks: toolResults,
		})
	}

	result := agent.LatestAssistantText(messages)
	if strings.TrimSpace(result) == "" {
		if finished {
			result = "(no summary)"
		} else {
			result = "Subagent stopped after 30 turns without final answer."
		}
	}

	logger.Info("[Subagent] done")
	return strings.TrimSpace(result), nil
}
