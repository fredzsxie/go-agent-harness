// Package model 提供 LLM 模型接口的具体适配实现。
package model

import (
	"context"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/config"
)

// Anthropic 使用 Anthropic Messages API 实现 agent.Model。
type Anthropic struct {
	client anthropic.Client
	model  string
}

var _ agent.Model = (*Anthropic)(nil)

func NewAnthropic(cfg config.LLMConfig) *Anthropic {
	return &Anthropic{
		client: anthropic.NewClient(
			option.WithBaseURL(cfg.BaseURL),
			option.WithAPIKey(cfg.APIKey),
		),
		model: cfg.Model,
	}
}

// Complete 将内部统一请求转换为 Anthropic 请求，并把响应还原为 Agent 消息。
func (m *Anthropic) Complete(ctx context.Context, request agent.ModelRequest) (agent.ModelResponse, error) {
	response, err := m.client.Messages.New(ctx, anthropic.MessageNewParams{
		MaxTokens: request.MaxTokens,
		Model:     anthropic.Model(m.model),
		Messages:  toAnthropicMessages(request.Messages),
		Tools:     toAnthropicTools(request.Tools),
		System:    systemBlocks(request.System),
	})
	if err != nil {
		return agent.ModelResponse{}, err
	}
	return agent.ModelResponse{Message: parseAssistantMessage(response.Content)}, nil
}

func systemBlocks(system string) []anthropic.TextBlockParam {
	if system == "" {
		return nil
	}
	return []anthropic.TextBlockParam{{Text: system}}
}
