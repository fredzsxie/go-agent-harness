package main

import (
	"context"
	"fmt"
	"os"

	"go-agent-harness/config"
	"go-agent-harness/internal/app"
)

func main() {
	if err := config.LoadEnvFile(".env"); err != nil {
		exit(err)
	}
	cfg, err := config.LoadLLMConfig()
	if err != nil {
		exit(err)
	}
	if err := app.New(cfg, os.Stdin, os.Stdout).Run(context.Background()); err != nil {
		exit(err)
	}
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
