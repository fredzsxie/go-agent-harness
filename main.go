package main

import (
	"fmt"

	"go-agent-harness/config"
)

func main() {
	cfg := config.LLMConfig{
		BaseURL: "http://localhost:8000/v1",
		APIKey:  "",
		Model:   "claude-sonnet-4-6",
	}

	fmt.Printf("LLM config: base_url=%s model=%s api_key_set=%t\n", cfg.BaseURL, cfg.Model, cfg.APIKey != "")
}
