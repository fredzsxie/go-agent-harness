package subagent

import (
	"context"
	"fmt"
	"strings"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/prompt"
	"go-agent-harness/internal/protocol"
	"go-agent-harness/internal/tool"
)

const maxTurns = 30

type Manager struct {
	worker *agent.Worker
}

func New(model llm.Model, registry *tool.Registry, hookManager *hooks.Manager) *Manager {
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

// spawn 对应 s06：每次委派创建新历史，只把最终摘要作为父 Agent 的一个 tool_result 返回。
// 文件系统与父 Agent 共享，隔离的是消息和工具权限；它不是沙箱，也不是 s13 的持久 Teammate。
// 受限工具集合由 app 注入，不复制主 Agent 的 Memory、Cron、MCP 或递归委派能力。
func (m *Manager) spawn(ctx context.Context, description string) (string, error) {
	logger.Info("[Subagent] spawned")

	messages := []protocol.Message{{Role: protocol.RoleUser, Content: description}}
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

		messages = append(messages, protocol.Message{
			Role:   protocol.RoleUser,
			Blocks: toolResults,
		})
	}

	result := protocol.LatestAssistantText(messages)
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
