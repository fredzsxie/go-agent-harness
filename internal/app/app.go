package app

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chzyer/readline"

	"go-agent-harness/config"
	"go-agent-harness/internal/prompt"
	agentruntime "go-agent-harness/internal/runtime"
	"go-agent-harness/internal/skill"
	"go-agent-harness/internal/subagent"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/todo"
	"go-agent-harness/loop"
)

type App struct {
	runner         *loop.Runner
	cron           *agentruntime.CronScheduler
	messages       []loop.Message
	agentMu        sync.Mutex
	nonInteractive *atomic.Bool
	in             io.Reader
	out            io.Writer
}

func New(cfg config.LLMConfig, in io.Reader, out io.Writer) *App {
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}
	nonInteractive := &atomic.Bool{}

	// initial basic tools & hooks
	registry := newDefaultRegistry()
	hookManager := newDefaultHooks(out, nonInteractive)

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

	// initial cron tool
	cronManager := agentruntime.NewCron(agentruntime.CronConfig{WorkDir: workDir})
	registerCronTools(registry, cronManager)

	// init skill list
	skillManager := skill.New()
	skillCatalog := skillManager.ListSkills()
	registerSkillTool(registry, skillManager)

	// init context compact module
	registerCompactTool(registry)

	return &App{
		runner:         loop.NewRunnerWithPromptBuilder(cfg, registry, hookManager, prompt.NewBuilder(skillCatalog, workDir)),
		cron:           cronManager,
		messages:       make([]loop.Message, 0, 16),
		nonInteractive: nonInteractive,
		in:             in,
		out:            out,
	}
}

func (a *App) Run(ctx context.Context) error {
	// 无论正常退出还是读取失败，都要停止并回收后台命令。
	defer a.runner.Close()
	stopCron := a.startCronRuntime(ctx)
	defer stopCron()

	fmt.Fprintln(a.out, "go-agent-harness")
	fmt.Fprintln(a.out, "Type a task, or type q/exit to quit.")

	if stdin, stdout, ok := interactiveStreams(a.in, a.out); ok {
		return a.runInteractive(ctx, stdin, stdout)
	}
	return a.runScanner(ctx)
}

// runInteractive uses readline for real terminals so cursor movement,
// backspace, and wide Unicode characters are rendered consistently.
func (a *App) runInteractive(ctx context.Context, stdin, stdout *os.File) error {
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

		exit, err := a.processInput(ctx, userInput)
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
func (a *App) runScanner(ctx context.Context) error {
	scanner := bufio.NewScanner(a.in)

	// REPL: Read-Eval-Print Loop
	for {
		fmt.Fprint(a.out, "> ")
		if !scanner.Scan() {
			break
		}

		exit, err := a.processInput(ctx, scanner.Text())
		if err != nil {
			return err
		}
		if exit {
			return nil
		}
	}

	return scanner.Err()
}

func (a *App) processInput(ctx context.Context, rawInput string) (bool, error) {
	userInput := strings.TrimSpace(rawInput)
	if shouldExit(userInput) {
		return true, nil
	}
	if userInput == "" {
		return false, nil
	}

	// 用户 turn 与定时 turn 共用同一会话，通过互斥锁保证一次只运行一个 Agent Loop。
	a.agentMu.Lock()
	defer a.agentMu.Unlock()
	a.messages = append(a.messages, loop.Message{Role: loop.RoleUser, Content: userInput})
	result, err := a.runner.Run(ctx, a.messages)
	if err != nil {
		return false, err
	}
	a.messages = result.Messages
	if result.Output != "" {
		fmt.Fprintln(a.out, result.Output)
	}
	return false, nil
}

// startCronRuntime 分离“检查到期时间”和“等待 Agent 空闲后投递”两个循环。
func (a *App) startCronRuntime(parent context.Context) func() {
	if err := a.cron.Load(); err != nil {
		fmt.Fprintf(a.out, "  [cron] %v\n", err)
	}
	ctx, cancel := context.WithCancel(parent)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case moment := <-ticker.C:
				a.cron.Poll(moment)
			}
		}
	}()
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if a.cron.HasPending() && a.agentMu.TryLock() {
					func() {
						defer a.agentMu.Unlock()
						a.runScheduledTurn(ctx)
					}()
				}
			}
		}
	}()
	return func() {
		cancel()
		wg.Wait()
	}
}

// runScheduledTurn 在持有 agentMu 时投递到期提示；调用失败则恢复队列以便至少投递一次。
func (a *App) runScheduledTurn(ctx context.Context) {
	jobs := a.cron.Consume()
	if len(jobs) == 0 {
		return
	}
	start := len(a.messages)
	for _, job := range jobs {
		a.messages = append(a.messages, loop.Message{Role: loop.RoleUser, Content: "[Scheduled] " + job.Prompt})
		fmt.Printf("  [cron] delivered %s: %s\n", job.ID, previewText(job.Prompt, 60))
	}

	// 定时 turn 无人值守，权限 hook 不得读取终端等待人工确认。
	a.nonInteractive.Store(true)
	result, err := func() (loop.RunResult, error) {
		defer a.nonInteractive.Store(false)
		return a.runner.Run(ctx, a.messages)
	}()
	if err != nil {
		a.messages = a.messages[:start]
		a.cron.Restore(jobs)
		fmt.Fprintf(a.out, "  [cron] delivery failed: %v\n", err)
		return
	}
	a.messages = result.Messages
	if err := a.cron.Acknowledge(jobs); err != nil {
		fmt.Fprintf(a.out, "  [cron] acknowledgement failed: %v\n", err)
	}
	if result.Output != "" {
		fmt.Fprintln(a.out, result.Output)
	}
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
