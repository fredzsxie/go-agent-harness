package main

import (
	"context"
	"fmt"
	"os"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"go-agent-harness/config"
)

func main() {
	cfg := config.LLMConfig{
		BaseURL: os.Getenv("ANTHROPIC_BASE_URL"),
		APIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		Model:   "claude-sonnet-4-6",
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com"
	}
	if cfg.APIKey == "" {
		panic("ANTHROPIC_API_KEY is required")
	}

	client := anthropic.NewClient(
		option.WithBaseURL(cfg.BaseURL),
		option.WithAPIKey(cfg.APIKey),
	)

	resp, err := client.Messages.New(context.Background(), anthropic.MessageNewParams{
		MaxTokens: 64,
		Model:     cfg.Model,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("Say hello in one short sentence.")),
		},
	})
	if err != nil {
		panic(err)
	}

	for _, block := range resp.Content {
		if text := block.AsText(); text.Text != "" {
			fmt.Println(text.Text)
		}
	}
}
