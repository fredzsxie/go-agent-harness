package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/protocol"
)

const defaultWorkflowMaxTokens int64 = 2000

const workflowAgentSystemPrompt = "You are a focused workflow agent. Complete only the supplied step. Do not claim access to files, tools, or results not included in the prompt."

type AgentResult struct {
	Value  any
	Tokens int64
}

type AgentRunner interface {
	Run(context.Context, string, map[string]any, string) (AgentResult, error)
}

// ModelRunner 复用 Host 的模型接口，但不向 Workflow agent 暴露任何工具。
type ModelRunner struct {
	model     agent.Model
	maxTokens int64
}

func NewModelRunner(model agent.Model, maxTokens int64) *ModelRunner {
	if maxTokens <= 0 {
		maxTokens = defaultWorkflowMaxTokens
	}
	return &ModelRunner{model: model, maxTokens: maxTokens}
}

func (r *ModelRunner) Run(ctx context.Context, prompt string, schema map[string]any, _ string) (AgentResult, error) {
	if r == nil || r.model == nil {
		return AgentResult{}, fmt.Errorf("workflow agent model is required")
	}
	request := prompt
	if schema != nil {
		rawSchema, err := json.Marshal(schema)
		if err != nil {
			return AgentResult{}, err
		}
		request += "\n\nReturn only valid JSON matching this schema:\n" + string(rawSchema)
	}
	response, err := r.model.Complete(ctx, agent.ModelRequest{
		System:    workflowAgentSystemPrompt,
		Messages:  []protocol.Message{{Role: protocol.RoleUser, Content: request}},
		MaxTokens: r.maxTokens,
	})
	if err != nil {
		return AgentResult{}, err
	}
	text := strings.TrimSpace(response.Message.Content)
	value := any(text)
	if schema != nil {
		if parsed, err := parseJSONOutput(text); err == nil {
			value = parsed
		}
	}
	return AgentResult{
		Value:  value,
		Tokens: response.Usage.InputTokens + response.Usage.OutputTokens,
	}, nil
}

func parseJSONOutput(text string) (any, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) > 0 {
			lines = lines[1:]
		}
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
			lines = lines[:len(lines)-1]
		}
		text = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	decoder := json.NewDecoder(bytes.NewBufferString(text))
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("workflow agent returned invalid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("workflow agent returned trailing JSON content")
	}
	return value, nil
}
