package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chzyer/readline"

	"go-agent-harness/config"
	"go-agent-harness/internal/prompt"
	"go-agent-harness/internal/skill"
	"go-agent-harness/internal/subagent"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/todo"
	"go-agent-harness/loop"
)

type App struct {
	runner *loop.Runner
	in     io.Reader
	out    io.Writer
}

func New(cfg config.LLMConfig, in io.Reader, out io.Writer) *App {
	// initial basic tools & hooks
	registry := newDefaultRegistry()
	hookManager := newDefaultHooks(out)

	// initial todo_write
	todoManager := todo.NewManager(out)
	registerTodoTool(registry, todoManager)

	// initial subagent
	subRegistry := newSubagentRegistry()
	subagentManager := subagent.New(cfg, subRegistry, hookManager, out)
	registerTaskTool(registry, subagentManager)

	// initial task_system
	taskManager := task.New(task.Config{})
	registerTaskSystemTools(registry, taskManager)

	// init skill list
	skillManager := skill.New()
	skillCatalog := skillManager.ListSkills()
	registerSkillTool(registry, skillManager)

	// init context compact module
	registerCompactTool(registry)

	workspace, err := os.Getwd()
	if err != nil {
		workspace = "."
	}
	return &App{
		runner: loop.NewRunnerWithPromptBuilder(cfg, registry, hookManager, prompt.NewBuilder(skillCatalog, workspace)),
		in:     in,
		out:    out,
	}
}

func (a *App) Run(ctx context.Context) error {
	messages := make([]loop.Message, 0, 16)

	fmt.Fprintln(a.out, "go-agent-harness")
	fmt.Fprintln(a.out, "Type a task, or type q/exit to quit.")

	if stdin, stdout, ok := interactiveStreams(a.in, a.out); ok {
		return a.runInteractive(ctx, messages, stdin, stdout)
	}
	return a.runScanner(ctx, messages)
}

// runInteractive uses readline for real terminals so cursor movement,
// backspace, and wide Unicode characters are rendered consistently.
func (a *App) runInteractive(ctx context.Context, messages []loop.Message, stdin, stdout *os.File) error {
	lineReader, err := readline.NewEx(&readline.Config{
		Prompt:                 "> ",
		InterruptPrompt:        "^C",
		EOFPrompt:              "exit",
		DisableAutoSaveHistory: true,
		Stdin:                  stdin,
		Stdout:                 stdout,
		Stderr:                 stdout,
	})
	if err != nil {
		return err
	}
	defer lineReader.Close()

	for {
		userInput, err := lineReader.Readline()
		if err == readline.ErrInterrupt {
			if len(userInput) == 0 {
				return nil
			}
			continue
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		exit, err := a.processInput(ctx, userInput, &messages)
		if err != nil {
			return err
		}
		if exit {
			return nil
		}
	}
}

// runScanner keeps stdin-piped and test-driven execution line-oriented. A
// readline terminal requires a real TTY and raw-mode support, neither of
// which is available for a bytes.Buffer or a redirected stdin.
func (a *App) runScanner(ctx context.Context, messages []loop.Message) error {
	scanner := bufio.NewScanner(a.in)

	// REPL: Read-Eval-Print Loop
	for {
		fmt.Fprint(a.out, "> ")
		if !scanner.Scan() {
			break
		}

		exit, err := a.processInput(ctx, scanner.Text(), &messages)
		if err != nil {
			return err
		}
		if exit {
			return nil
		}
	}

	return scanner.Err()
}

func (a *App) processInput(ctx context.Context, rawInput string, messages *[]loop.Message) (bool, error) {
	userInput := strings.TrimSpace(rawInput)
	if shouldExit(userInput) {
		return true, nil
	}
	if userInput == "" {
		return false, nil
	}

	*messages = append(*messages, loop.Message{Role: loop.RoleUser, Content: userInput})
	result, err := a.runner.Run(ctx, *messages)
	if err != nil {
		return false, err
	}
	*messages = result.Messages
	if result.Output != "" {
		fmt.Fprintln(a.out, result.Output)
	}
	return false, nil
}

func interactiveStreams(in io.Reader, out io.Writer) (stdin, stdout *os.File, ok bool) {
	stdin, inOK := in.(*os.File)
	stdout, outOK := out.(*os.File)
	if !inOK || !outOK || !isCharacterDevice(stdin) || !isCharacterDevice(stdout) {
		return nil, nil, false
	}
	return stdin, stdout, true
}

func isCharacterDevice(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func shouldExit(input string) bool {
	switch strings.ToLower(strings.TrimSpace(input)) {
	case "q", "quit", "exit":
		return true
	default:
		return false
	}
}
