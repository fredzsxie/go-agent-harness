// Package model 提供 LLM 模型接口的具体适配实现。
package model

import (
	"context"
	"errors"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"go-agent-harness/internal/config"
	"go-agent-harness/internal/llm"
)

// Anthropic 使用 Anthropic Messages API 实现 llm.Model。
type Anthropic struct {
	client anthropic.Client
	model  string
}

var _ llm.Model = (*Anthropic)(nil)

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
func (m *Anthropic) Complete(ctx context.Context, request llm.Request) (llm.Response, error) {
	modelID := request.Model
	if modelID == "" {
		modelID = m.model
	}
	response, err := m.client.Messages.New(ctx, anthropic.MessageNewParams{
		MaxTokens: request.MaxTokens,
		Model:     anthropic.Model(modelID),
		Messages:  toAnthropicMessages(request.Messages),
		Tools:     toAnthropicTools(request.Tools),
		System:    systemBlocks(request.System),
	})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return llm.Response{}, &llm.Error{HTTPStatus: apiErr.StatusCode, Err: err}
		}
		return llm.Response{}, err
	}
	return parseModelResponse(response), nil
}

// parseModelResponse 将 Anthropic 响应及 token 用量转换为项目内部统一结构。
func parseModelResponse(response *anthropic.Message) llm.Response {
	return llm.Response{
		Message:    parseAssistantMessage(response.Content),
		StopReason: string(response.StopReason),
		Usage: llm.Usage{
			InputTokens:  response.Usage.InputTokens,
			OutputTokens: response.Usage.OutputTokens,
		},
	}
}

func systemBlocks(system string) []anthropic.TextBlockParam {
	if system == "" {
		return nil
	}
	return []anthropic.TextBlockParam{{Text: system}}
}
