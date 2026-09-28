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
	"go-agent-harness/internal/config"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/mcp"
	llmmodel "go-agent-harness/internal/model"
	"go-agent-harness/internal/prompt"
	"go-agent-harness/internal/protocol"
	agentruntime "go-agent-harness/internal/runtime"
	"go-agent-harness/internal/skill"
	"go-agent-harness/internal/subagent"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/team"
	"go-agent-harness/internal/todo"
	"go-agent-harness/internal/workflow"
	"go-agent-harness/internal/worktree"
)

type App struct {
	session appSession
	cron    *agentruntime.CronScheduler
	team    *team.Runtime
	in      io.Reader
	out     io.Writer
}

type appSession interface {
	Submit(context.Context, ...protocol.Message) (agent.RunResult, error)
	TrySubmit(context.Context, ...protocol.Message) (agent.RunResult, bool, error)
	BackgroundReady() <-chan struct{}
	HasBackgroundResults() bool
	Close()
}

func New(cfg config.LLMConfig, in io.Reader, out io.Writer) *App {
	logger.SetOutput(out)
	workDir, err := os.Getwd()
	if err != nil {
		workDir = "."
	}
	model := llmmodel.NewAnthropic(cfg)

	// 先组装基础工具与 Hooks，再按章节能力扩展主 Agent 工具池。
	registry := newDefaultRegistry()
	mcpManager := mcp.New(registry)
	registerMCPTool(registry, mcpManager)
	hookManager := newDefaultHooks(mcpManager)

	// TodoWrite 只维护当前 Session 的临时计划。
	todoManager := todo.NewManager(out)
	registerTodoTool(registry, todoManager)

	// Subagent 使用独立且受限的工具池。
	subRegistry := newSubagentRegistry()
	subagentManager := subagent.New(model, subRegistry, hookManager)
	registerTaskTool(registry, subagentManager)

	// Task System 维护跨 Session 的持久任务图。
	taskManager := task.New(task.Config{WorkDir: workDir})
	registerTaskSystemTools(registry, taskManager)

	// Team Runtime 复用受限基础工具，但为每个 Teammate 动态绑定 Task Workspace。
	teamBus := team.NewBus(team.BusConfig{WorkDir: workDir})
	teamRequests := team.NewRequests()
	var teamRuntime *team.Runtime
	worktreeManager := worktree.New(worktree.Config{
		WorkDir: workDir, Tasks: taskManager,
		InUse: func(path string) bool { return teamRuntime != nil && teamRuntime.InUse(path) },
	})
	teamRuntime = team.NewRuntime(team.RuntimeConfig{
		Model: model, BaseTools: subRegistry, Tasks: taskManager,
		Worktrees: worktreeManager, Bus: teamBus, Requests: teamRequests,
	})
	registerTeamTools(registry, teamRuntime, worktreeManager)

	// Cron Scheduler 只注册到主 Agent。
	cronManager := agentruntime.NewCron(agentruntime.CronConfig{WorkDir: workDir})
	registerCronTools(registry, cronManager)

	// Workflow 由 Host 注册可信脚本；主 Agent 只选择名称、参数和可选的恢复 run ID。
	workflowRegistry := workflow.NewRegistry()
	if err := workflow.RegisterDefaults(workflowRegistry); err != nil {
		panic(err)
	}
	workflowManager := workflow.NewManager(workflow.ManagerConfig{
		Registry: workflowRegistry,
		Store:    workflow.NewStore(workflow.StoreConfig{WorkDir: workDir}),
		Runner:   workflow.NewModelRunner(model, 0),
	})
	registerWorkflowTool(registry, workflowManager)

	// System Prompt 常驻 Skill 目录，正文由工具按需加载。
	skillManager := skill.New()
	skillCatalog := skillManager.ListSkills()
	registerSkillTool(registry, skillManager)

	// Compact 是由 Runner 处理会话状态的控制工具。
	registerCompactTool(registry)

	runner := agent.NewRunnerWithPromptBuilder(
		model, registry, hookManager, prompt.NewBuilder(skillCatalog, workDir),
		agent.WithFallbackModel(cfg.FallbackModel),
		agent.WithLiveContext(func() prompt.LiveContext {
			teammates := teamRuntime.List()
			names := make([]string, 0, len(teammates))
			for _, teammate := range teammates {
				names = append(names, teammate.Name)
			}
			return prompt.LiveContext{
				ConnectedMCP:    mcpManager.Connected(),
				ActiveTeammates: names,
			}
		}),
	)
	return &App{
		session: agent.NewSession(runner),
		cron:    cronManager,
		team:    teamRuntime,
		in:      in,
		out:     out,
	}
}

func (a *App) Run(ctx context.Context) error {
	// defer 逆序停止事件投递、Cron、Teammate 和 Session，避免退出期间再进入 Agent Loop。
	defer a.session.Close()
	if a.team != nil {
		defer a.team.Close()
	}
	stopAsync := a.startAsyncRuntime(ctx)
	defer stopAsync()

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

	result, err := a.session.Submit(ctx, protocol.Message{Role: protocol.RoleUser, Content: userInput})
	if err != nil {
		return false, err
	}
	if result.Output != "" {
		fmt.Fprintln(a.out, result.Output)
	}
	return false, nil
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
