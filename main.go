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
	// 日志配置属于进程入口，创建 App 不应改写其他实例的输出。
	logger.SetOutput(os.Stdout)
	cfg, err := config.LoadLLMConfig()
	if err != nil {
		exit(err)
	}
	application, err := app.New(cfg, os.Stdin, os.Stdout)
	if err != nil {
		exit(err)
	}
	if err := application.Run(context.Background()); err != nil {
		exit(err)
	}
}

func exit(err error) {
	logger.Error("%v", err)
	os.Exit(1)
}
