package app

import (
	"context"
	"fmt"
	"io"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/goal"
	"go-agent-harness/internal/protocol"
	agentruntime "go-agent-harness/internal/runtime"
	"go-agent-harness/internal/team"
)

type App struct {
	session  appSession
	cron     *agentruntime.CronScheduler
	team     *team.Runtime
	goal     *goal.Controller
	readLine func(string) (string, error)
	in       io.Reader
	out      io.Writer
}

type appSession interface {
	Submit(context.Context, ...protocol.Message) (agent.RunResult, error)
	TrySubmit(context.Context, ...protocol.Message) (agent.RunResult, bool, error)
	BackgroundReady() <-chan struct{}
	HasBackgroundResults() bool
	TotalTokens() int64
	Close()
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
