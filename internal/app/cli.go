package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chzyer/readline"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/goal"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/protocol"
	"go-agent-harness/internal/team"
)

// runInteractive 与审批共用同一个 readline 实例，避免两个读取器竞争或预读终端输入。
func (a *App) runInteractive(ctx context.Context, stdin, stdout *os.File) error {
	reader, err := readline.NewEx(&readline.Config{
		Prompt: "> ", InterruptPrompt: "^C", EOFPrompt: "exit", DisableAutoSaveHistory: true,
		Stdin: stdin, Stdout: stdout, Stderr: stdout,
	})
	if err != nil {
		return err
	}
	defer reader.Close()
	a.readLine = func(prompt string) (string, error) { reader.SetPrompt(prompt); return reader.Readline() }
	defer func() { a.readLine = nil }()
	return a.runREPL(ctx)
}

// runScanner 也与审批共享缓冲区，重定向输入中的确认行不会被另一个 Reader 吞掉。
func (a *App) runScanner(ctx context.Context) error {
	reader := bufio.NewReader(a.in)
	a.readLine = func(prompt string) (string, error) {
		fmt.Fprint(a.out, prompt)
		line, err := reader.ReadString('\n')
		if err == io.EOF && len(line) > 0 {
			err = nil
		}
		return strings.TrimRight(line, "\r\n"), err
	}
	defer func() { a.readLine = nil }()
	return a.runREPL(ctx)
}

func (a *App) runREPL(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		input, err := a.readLine("> ")
		if err == readline.ErrInterrupt {
			if len(input) == 0 {
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
		exit, err := a.processInput(ctx, input)
		if err != nil || exit {
			return err
		}
	}
}

// askApproval 是权限层注入的前台交互适配器，自动回合在进入此函数前就会被拒绝。
func (a *App) askApproval(toolName string, args map[string]any, reason string) bool {
	if a.readLine == nil {
		return false
	}
	fmt.Fprintf(a.out, "\n⚠  %s\n   Tool: %s(%v)\n", reason, toolName, args)
	choice, err := a.readLine("   Allow? [y/N] ")
	if err != nil {
		return false
	}
	choice = strings.ToLower(strings.TrimSpace(choice))
	return choice == "y" || choice == "yes"
}

func (a *App) processInput(ctx context.Context, rawInput string) (bool, error) {
	userInput := strings.TrimSpace(rawInput)
	if shouldExit(userInput) {
		return true, nil
	}
	if userInput == "" {
		return false, nil
	}
	if a.goal != nil {
		if userInput == "/goal" {
			fmt.Fprintln(a.out, a.goal.Status(a.session.TotalTokens()))
			return false, nil
		}
		if strings.HasPrefix(userInput, "/goal ") {
			condition := strings.TrimSpace(strings.TrimPrefix(userInput, "/goal "))
			if isGoalClearAlias(condition) {
				if cleared, ok := a.goal.Clear("cleared by user"); ok {
					fmt.Fprintf(a.out, "Goal cleared: %s\n", cleared.Condition)
				} else {
					fmt.Fprintln(a.out, "No goal set")
				}
				return false, nil
			}
			if _, err := a.goal.Set(condition, a.session.TotalTokens()); err != nil {
				fmt.Fprintf(a.out, "[goal] error: %v\n", err)
				return false, nil
			}
			userInput = condition
		}
		a.goal.BeginQuery()
	}

	result, err := a.session.Submit(ctx, protocol.Message{Role: protocol.RoleUser, Content: userInput})
	if err != nil {
		return false, err
	}
	printRunResult(a.out, result)
	return false, nil
}

func isGoalClearAlias(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "clear", "stop", "off", "reset", "none", "cancel":
		return true
	default:
		return false
	}
}

func activeGoalCondition(controller *goal.Controller) string {
	if controller == nil {
		return ""
	}
	state, ok := controller.Active()
	if !ok {
		return ""
	}
	return state.Condition
}

func printRunResult(output io.Writer, result agent.RunResult) {
	if output == nil {
		return
	}
	if result.Output != "" {
		fmt.Fprintln(output, result.Output)
	}
	if result.Stop.Action != "" && result.Stop.Action != hooks.StopAllow {
		fmt.Fprintf(output, "[goal] %s: %s\n", result.Stop.Action, result.Stop.Reason)
	}
}

func goalPendingReason(backgroundRunning bool, teammates []team.TeammateInfo) string {
	for _, teammate := range teammates {
		if teammate.Status == team.TeammateWaitingApproval {
			return fmt.Sprintf("teammate %q is waiting for approval", teammate.Name)
		}
	}
	if backgroundRunning {
		return "background work is still running"
	}
	for _, teammate := range teammates {
		if teammate.Status == team.TeammateWorking || teammate.Status == team.TeammateStopping {
			return fmt.Sprintf("teammate %q is still %s", teammate.Name, teammate.Status)
		}
	}
	return ""
}

func previewText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
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
