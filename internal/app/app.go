package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"go-agent-harness/config"
	"go-agent-harness/internal/subagent"
	"go-agent-harness/internal/todo"
	"go-agent-harness/loop"
)

type App struct {
	runner *loop.Runner
	in     io.Reader
	out    io.Writer
}

func New(cfg config.LLMConfig, in io.Reader, out io.Writer) *App {
	registry := newDefaultRegistry()
	hookManager := newDefaultHooks(out)

	subRegistry := newSubagentRegistry()
	subagentManager := subagent.New(cfg, subRegistry, hookManager, out)

	todoManager := todo.NewManager(out)
	registerTodoTool(registry, todoManager)
	registerTaskTool(registry, subagentManager)

	return &App{
		runner: loop.NewRunner(cfg, registry, hookManager),
		in:     in,
		out:    out,
	}
}

func (a *App) Run(ctx context.Context) error {
	scanner := bufio.NewScanner(a.in)
	messages := make([]loop.Message, 0, 16)

	fmt.Fprintln(a.out, "go-agent-harness")
	fmt.Fprintln(a.out, "Type a task, or type q/exit to quit.")

	// REPL: Read-Eval-Print Loop
	for {
		fmt.Fprint(a.out, "> ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if shouldExit(userInput) {
			break
		}
		if userInput == "" {
			continue
		}

		messages = append(messages, loop.Message{Role: loop.RoleUser, Content: userInput})
		result, err := a.runner.Run(ctx, messages)
		if err != nil {
			return err
		}
		messages = result.Messages
		if result.Output != "" {
			fmt.Fprintln(a.out, result.Output)
		}
	}

	return scanner.Err()
}

func shouldExit(input string) bool {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "q", "quit", "exit":
		return true
	default:
		return false
	}
}
