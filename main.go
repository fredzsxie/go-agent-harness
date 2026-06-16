package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"go-agent-harness/config"
)

func main() {
	loadEnvFile(".env")

	cfg := config.LLMConfig{
		BaseURL: os.Getenv("ANTHROPIC_BASE_URL"),
		APIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		Model:   os.Getenv("MODEL_ID"),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api.anthropic.com"
	}
	if cfg.Model == "" {
		cfg.Model = "claude-sonnet-4-6"
	}
	if cfg.APIKey == "" {
		panic("ANTHROPIC_API_KEY is required")
	}
	fmt.Println(os.Getenv("ANTHROPIC_BASE_URL"), os.Getenv("ANTHROPIC_API_KEY"))

	client := anthropic.NewClient(
		option.WithBaseURL(cfg.BaseURL),
		option.WithAPIKey(cfg.APIKey),
	)

	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock("你现在使用的是什么模型？")),
	}

	resp, err := client.Messages.New(context.Background(), anthropic.MessageNewParams{
		MaxTokens: 2000,
		Model:     cfg.Model,
		Messages:  messages,
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

func loadEnvFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(strings.TrimSpace(key), strings.TrimSpace(value))
	}
}
