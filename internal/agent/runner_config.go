package agent

import (
	"strings"

	"go-agent-harness/internal/agentctx"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/prompt"
	agentruntime "go-agent-harness/internal/runtime"
	"go-agent-harness/internal/tool"
)

// Runner 的可变上下文由 Session 串行访问；后台命令状态由 BackgroundManager 独立同步。
type Runner struct {
	worker        *Worker
	registry      *tool.Registry
	hooks         *hooks.Manager
	context       *agentctx.Manager
	background    *agentruntime.BackgroundManager
	fallbackModel string
	recovery      recoveryPolicy
	liveContext   func() prompt.LiveContext
	maxTurns      int
}

type RunnerOption func(*Runner)

// WithFallbackModel 配置主模型持续 overloaded 时使用的备用模型。
func WithFallbackModel(model string) RunnerOption {
	return func(r *Runner) { r.fallbackModel = strings.TrimSpace(model) }
}

// WithLiveContext 注入每轮模型调用前读取的实时运行状态。
func WithLiveContext(provider func() prompt.LiveContext) RunnerOption {
	return func(r *Runner) { r.liveContext = provider }
}

// WithMaxTurns 设置单次 Run 最多允许的主模型调用次数；0 表示不限制。
func WithMaxTurns(maxTurns int) RunnerOption {
	return func(r *Runner) {
		if maxTurns > 0 {
			r.maxTurns = maxTurns
		}
	}
}

func NewRunner(model llm.Model, registry *tool.Registry, hookManager *hooks.Manager, systemPrompt string, options ...RunnerOption) *Runner {
	if hookManager == nil {
		hookManager = hooks.NewManager()
	}
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt = prompt.Main("")
	}
	runner := &Runner{
		registry: registry,
		hooks:    hookManager,
		recovery: defaultRecoveryPolicy(),
	}
	for _, option := range options {
		option(runner)
	}
	if runner.context == nil {
		runner.context = agentctx.New(agentctx.Config{Model: model, SystemPrompt: systemPrompt})
	}
	runner.background = newBackgroundManager(registry)
	runner.worker = NewWorker(model, registry, hookManager)
	return runner
}

// WithContextManager 注入已绑定工作区的上下文生命周期，便于独立测试和不同工作区复用。
func WithContextManager(manager *agentctx.Manager) RunnerOption {
	return func(r *Runner) { r.context = manager }
}
