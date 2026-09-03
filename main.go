// Command agent 启动交互式 coding agent harness。
package main

import (
	"context"
	"os"

	"go-agent-harness/internal/app"
	"go-agent-harness/internal/config"
	"go-agent-harness/internal/logger"
)

func main() {
	if err := config.LoadEnvFile(".env"); err != nil {
		exit(err)
	}
	mode, err := logger.ParseMode(os.Getenv("LOG_MODE"))
	if err != nil {
		exit(err)
	}
	logger.SetMode(mode)
	cfg, err := config.LoadLLMConfig()
	if err != nil {
		exit(err)
	}
	if err := app.New(cfg, os.Stdin, os.Stdout).Run(context.Background()); err != nil {
		exit(err)
	}
}

func exit(err error) {
	logger.Error("%v", err)
	os.Exit(1)
}
