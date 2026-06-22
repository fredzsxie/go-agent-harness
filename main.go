package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"go-agent-harness/config"
	"go-agent-harness/loop"
	"go-agent-harness/tools"
)

func main() {
	// 读取本地 .env，方便开发时注入 Anthropic 配置。
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

	registry := loop.NewRegistry()
	registry.Register("bash", tools.RunBash)
	registry.Register("read_file", tools.RunReadFile)
	registry.Register("write_file", tools.RunWriteFile)
	registry.Register("edit_file", tools.RunEditFile)
	registry.Register("glob", tools.RunGlob)

	runner := loop.NewRunner(cfg, registry)
	scanner := bufio.NewScanner(os.Stdin)

	// 保存整个会话的消息历史，保证多轮对话能继续上下文。
	messages := make([]loop.Message, 0, 16)

	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		// 处理用户输入
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" || prompt == "q" || prompt == "exit" {
			break
		}

		// 获取并打印LLM处理结果
		messages = append(messages, loop.Message{Role: loop.RoleUser, Content: prompt})
		output, err := runner.Run(context.Background(), messages)
		if err != nil {
			panic(err)
		}
		fmt.Println(output)

		// 追加至message list中
		messages = append(messages, loop.Message{Role: loop.RoleAssistant, Content: output})
	}

	if err := scanner.Err(); err != nil {
		panic(err)
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
		if _, exists := os.LookupEnv(strings.TrimSpace(key)); exists {
			continue
		}
		_ = os.Setenv(strings.TrimSpace(key), strings.TrimSpace(value))
	}
}
