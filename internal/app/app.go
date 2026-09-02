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

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/config"
	"go-agent-harness/internal/logger"
	llmmodel "go-agent-harness/internal/model"
	"go-agent-harness/internal/prompt"
	agentruntime "go-agent-harness/internal/runtime"
	"go-agent-harness/internal/skill"
	"go-agent-harness/internal/subagent"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/todo"
)

type App struct {
	session        *agent.Session
	cron           *agentruntime.CronScheduler
	nonInteractive *atomic.Bool
	in             io.Reader
	out            io.Writer
}

func New(cfg config.LLMConfig, in io.Reader, out io.Writer) *App {
	logger.SetOutput(out)
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}
	nonInteractive := &atomic.Bool{}
	model := llmmodel.NewAnthropic(cfg)

	// 先组装基础工具与 Hooks，再按章节能力扩展主 Agent 工具池。
	registry := newDefaultRegistry()
	hookManager := newDefaultHooks(nonInteractive)

	// TodoWrite 只维护当前 Session 的临时计划。
	todoManager := todo.NewManager(out)
	registerTodoTool(registry, todoManager)

	// Subagent 使用独立且受限的工具池。
	subRegistry := newSubagentRegistry()
	subagentManager := subagent.New(model, subRegistry, hookManager)
	registerTaskTool(registry, subagentManager)

	// Task System 维护跨 Session 的持久任务图。
	taskManager := task.New(task.Config{})
	registerTaskSystemTools(registry, taskManager)

	// Cron Scheduler 只注册到主 Agent。
	cronManager := agentruntime.NewCron(agentruntime.CronConfig{WorkDir: workDir})
	registerCronTools(registry, cronManager)

	// System Prompt 常驻 Skill 目录，正文由工具按需加载。
	skillManager := skill.New()
	skillCatalog := skillManager.ListSkills()
	registerSkillTool(registry, skillManager)

	// Compact 是由 Runner 处理会话状态的控制工具。
	registerCompactTool(registry)

	runner := agent.NewRunnerWithPromptBuilder(model, registry, hookManager, prompt.NewBuilder(skillCatalog, workDir))
	return &App{
		session:        agent.NewSession(runner),
		cron:           cronManager,
		nonInteractive: nonInteractive,
		in:             in,
		out:            out,
	}
}

func (a *App) Run(ctx context.Context) error {
	// 无论正常退出还是读取失败，都要停止并回收后台命令。
	defer a.session.Close()
	stopCron := a.startCronRuntime(ctx)
	defer stopCron()

	fmt.Fprintln(a.out, "go-agent-harness")
	fmt.Fprintln(a.out, "Type a task, or type q/exit to quit.")

	if stdin, stdout, ok := interactiveStreams(a.in, a.out); ok {
		return a.runInteractive(ctx, stdin, stdout)
	}
	return a.runScanner(ctx)
}

// runInteractive 对真实终端使用 readline，保证光标、退格和宽字符显示正确。
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

// runScanner 为重定向输入和测试保留逐行读取方式，这些输入不支持 TTY raw mode。
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

	result, err := a.session.Submit(ctx, agent.Message{Role: agent.RoleUser, Content: userInput})
	if err != nil {
		return false, err
	}
	if result.Output != "" {
		fmt.Fprintln(a.out, result.Output)
	}
	return false, nil
}

// startCronRuntime 分离“检查到期时间”和“等待 Agent 空闲后投递”两个循环。
func (a *App) startCronRuntime(parent context.Context) func() {
	if err := a.cron.Load(); err != nil {
		logger.Error("[cron] %v", err)
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
				if a.cron.HasPending() {
					a.runScheduledTurn(ctx)
				}
			}
		}
	}()
	return func() {
		cancel()
		wg.Wait()
	}
}

// runScheduledTurn 尝试向空闲 Session 投递到期提示；失败时恢复队列以保证至少投递一次。
func (a *App) runScheduledTurn(ctx context.Context) {
	jobs := a.cron.Consume()
	if len(jobs) == 0 {
		return
	}
	inputs := make([]agent.Message, 0, len(jobs))
	for _, job := range jobs {
		inputs = append(inputs, agent.Message{Role: agent.RoleUser, Content: "[Scheduled] " + job.Prompt})
		logger.Info("[cron] delivered %s: %s", job.ID, previewText(job.Prompt, 60))
	}

	// 权限模式只在成功占用 Session 后切换，避免影响正在执行的用户 turn。
	result, acquired, err := a.session.TrySubmit(ctx,
		func() { a.nonInteractive.Store(true) },
		func() { a.nonInteractive.Store(false) },
		inputs...,
	)
	if !acquired {
		a.cron.Restore(jobs)
		return
	}
	if err != nil {
		a.cron.Restore(jobs)
		logger.Error("[cron] delivery failed: %v", err)
		return
	}
	if err := a.cron.Acknowledge(jobs); err != nil {
		logger.Error("[cron] acknowledgement failed: %v", err)
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
