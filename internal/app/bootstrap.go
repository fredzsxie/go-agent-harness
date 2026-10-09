package app

import (
	"fmt"
	"io"
	"os"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/agentctx"
	"go-agent-harness/internal/compact"
	"go-agent-harness/internal/config"
	"go-agent-harness/internal/goal"
	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/mcp"
	llmmodel "go-agent-harness/internal/model"
	"go-agent-harness/internal/permission"
	"go-agent-harness/internal/prompt"
	agentruntime "go-agent-harness/internal/runtime"
	"go-agent-harness/internal/skill"
	"go-agent-harness/internal/subagent"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/team"
	"go-agent-harness/internal/todo"
	"go-agent-harness/internal/workflow"
	"go-agent-harness/internal/workspace"
	"go-agent-harness/internal/worktree"
)

// Config 是宿主装配入口。WorkDir 统一约束工具、技能和运行产物；Model 可替换为测试模型。
type Config struct {
	LLM     config.LLMConfig
	WorkDir string
	In      io.Reader
	Out     io.Writer
	Model   llm.Model
}

// New 保留 CLI 的简洁入口；初始化失败返回 error，由 main 决定如何展示。
func New(cfg config.LLMConfig, in io.Reader, out io.Writer) (*App, error) {
	return NewWithConfig(Config{LLM: cfg, In: in, Out: out})
}

// NewWithConfig 是 s15 的组合根：只在这里选择具体实现、生命周期和各角色可见的工具池。
// 功能模块不反向依赖 app；新增普通工具在所属模块声明 schema 与 handler，再在这里启用。
func NewWithConfig(settings Config) (*App, error) {
	cfg, in, out := settings.LLM, settings.In, settings.Out
	if in == nil || out == nil {
		return nil, fmt.Errorf("application input and output are required")
	}
	workDir := settings.WorkDir
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working directory: %w", err)
		}
	}
	resolver, err := workspace.New(workDir)
	if err != nil {
		return nil, err
	}
	workDir = resolver.Root()
	model := settings.Model
	if model == nil {
		model = llmmodel.NewAnthropic(cfg)
	}
	app := &App{in: in, out: out}
	authorizer := permission.New(resolver.Resolve, app.askApproval)
	// 先组装基础工具与 Hooks，再按章节能力扩展主 Agent 工具池。
	registry := newDefaultRegistry(resolver)
	mcpManager := mcp.New(registry)
	mcp.RegisterTool(registry, mcpManager)
	hookManager := newDefaultHooks(mcpManager, authorizer, workDir)

	// TodoWrite 只维护当前 Session 的临时计划。
	todoManager := todo.NewManager(out)
	todo.RegisterTool(registry, todoManager)

	// Subagent 使用独立且受限的工具池。
	subRegistry := newSubagentRegistry(resolver)
	subagentManager := subagent.New(model, subRegistry, hookManager)
	subagent.RegisterTool(registry, subagentManager)

	// Task System 维护跨 Session 的持久任务图。
	taskManager := task.New(task.Config{WorkDir: workDir})
	task.RegisterTools(registry, taskManager)

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
	// 后续装配失败也要取消 Team 的生命周期；还未运行时 Close 不会启动任何任务。
	initialized := false
	defer func() {
		if !initialized {
			teamRuntime.Close()
		}
	}()
	team.RegisterLeadTools(registry, teamRuntime)
	worktree.RegisterTool(registry, worktreeManager)

	// Cron Scheduler 只注册到主 Agent。
	cronManager := agentruntime.NewCron(agentruntime.CronConfig{WorkDir: workDir})
	agentruntime.RegisterCronTools(registry, cronManager)

	// Workflow 由 Host 注册可信脚本；主 Agent 只选择名称、参数和可选的恢复 run ID。
	workflowRegistry := workflow.NewRegistry()
	if err := workflow.RegisterDefaults(workflowRegistry); err != nil {
		return nil, err
	}
	workflowManager := workflow.NewManager(workflow.ManagerConfig{
		Registry: workflowRegistry,
		Store:    workflow.NewStore(workflow.StoreConfig{WorkDir: workDir}),
		Runner:   workflow.NewModelRunner(model, 0),
	})
	workflow.RegisterTool(registry, workflowManager)

	// System Prompt 常驻 Skill 目录，正文由工具按需加载。
	skillManager, err := skill.New(resolver)
	if err != nil {
		return nil, err
	}
	skillCatalog := skillManager.ListSkills()
	skill.RegisterTool(registry, skillManager)

	// Compact 是由 Runner 处理会话状态的控制工具。
	compact.RegisterTool(registry)
	var session *agent.Session
	goalEvaluator, err := goal.NewPromptEvaluator(model, cfg.GoalEvaluatorModel, 0)
	if err != nil {
		return nil, err
	}
	goalController, err := goal.New(goal.Config{
		Evaluator: goalEvaluator,
		BlockCap:  cfg.GoalStopBlockCap,
		PendingReason: func() string {
			backgroundRunning := session != nil && session.HasBackgroundWork()
			return goalPendingReason(backgroundRunning, teamRuntime.List())
		},
	})
	if err != nil {
		return nil, err
	}
	// Goal 是同一条 Agent Loop 上的 Stop gate，不创建第二个 Session。
	hookManager.OnStop(goalController.Stop)

	runner := agent.NewRunner(
		model, registry, hookManager, "",
		agent.WithContextManager(agentctx.New(agentctx.Config{WorkDir: workDir, Model: model, PromptBuilder: prompt.NewBuilder(skillCatalog, workDir)})),
		agent.WithFallbackModel(cfg.FallbackModel),
		agent.WithMaxTurns(cfg.MaxTurns),
		agent.WithLiveContext(func() prompt.LiveContext {
			teammates := teamRuntime.List()
			names := make([]string, 0, len(teammates))
			for _, teammate := range teammates {
				names = append(names, teammate.Name)
			}
			return prompt.LiveContext{
				ConnectedMCP:    mcpManager.Connected(),
				ActiveTeammates: names,
				GoalCondition:   activeGoalCondition(goalController),
			}
		}),
	)
	session = agent.NewSession(runner)
	app.session = session
	app.cron = cronManager
	app.team = teamRuntime
	app.goal = goalController
	initialized = true
	return app, nil
}
