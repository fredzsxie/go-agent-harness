// Package config 加载 Agent 进程所需的环境配置。
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type LLMConfig struct {
	BaseURL            string
	APIKey             string
	Model              string
	FallbackModel      string
	GoalEvaluatorModel string
	MaxTurns           int
	GoalStopBlockCap   int
}

const (
	DefaultBaseURL          = "https://api.anthropic.com"
	DefaultModel            = "claude-sonnet-4-6"
	DefaultGoalStopBlockCap = 8
)

func LoadLLMConfig() (LLMConfig, error) {
	cfg := LLMConfig{
		BaseURL:            strings.TrimSpace(os.Getenv("ANTHROPIC_BASE_URL")),
		APIKey:             strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")),
		Model:              strings.TrimSpace(os.Getenv("MODEL_ID")),
		FallbackModel:      strings.TrimSpace(os.Getenv("FALLBACK_MODEL_ID")),
		GoalEvaluatorModel: strings.TrimSpace(os.Getenv("GOAL_EVALUATOR_MODEL_ID")),
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if cfg.GoalEvaluatorModel == "" {
		cfg.GoalEvaluatorModel = cfg.Model
	}
	maxTurns, err := envInt("MAX_TURNS", 0, 0)
	if err != nil {
		return LLMConfig{}, err
	}
	blockCap, err := envInt("CLAUDE_CODE_STOP_HOOK_BLOCK_CAP", DefaultGoalStopBlockCap, 1)
	if err != nil {
		return LLMConfig{}, err
	}
	cfg.MaxTurns = maxTurns
	cfg.GoalStopBlockCap = blockCap
	if cfg.APIKey == "" {
		return LLMConfig{}, fmt.Errorf("ANTHROPIC_API_KEY is required")
	}
	return cfg, nil
}

func envInt(name string, fallback, minimum int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum {
		return 0, fmt.Errorf("%s must be an integer >= %d", name, minimum)
	}
	return parsed, nil
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
