// Package config 加载 Agent 进程所需的环境配置。
package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type LLMConfig struct {
	BaseURL       string
	APIKey        string
	Model         string
	FallbackModel string
}

const (
	DefaultBaseURL = "https://api.anthropic.com"
	DefaultModel   = "claude-sonnet-4-6"
)

func LoadLLMConfig() (LLMConfig, error) {
	cfg := LLMConfig{
		BaseURL:       strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL")),
		APIKey:        strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")),
		Model:         strings.TrimSpace(os.Getenv("MODEL_ID")),
		FallbackModel: strings.TrimSpace(os.Getenv("FALLBACK_MODEL_ID")),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.APIKey == "" {
		return LLMConfig{}, fmt.Errorf("ANTHROPIC_API_KEY is required")
	}
	return cfg, nil
}

func LoadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
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
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, strings.Trim(strings.TrimSpace(value), `"`))
	}
	return scanner.Err()
}
