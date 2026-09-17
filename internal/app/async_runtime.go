package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/permission"
	"go-agent-harness/internal/protocol"
	agentruntime "go-agent-harness/internal/runtime"
	"go-agent-harness/internal/team"
)

type asyncPending struct {
	teamEvents []team.Message
	cronJobs   []agentruntime.CronJob
	background bool
}

// startAsyncRuntime 将 Cron、Team 和后台命令的自动唤醒串行到同一个 Session 入口。
func (a *App) startAsyncRuntime(parent context.Context) func() {
	if a.cron != nil {
		if err := a.cron.Load(); err != nil {
			logger.Error("[AsyncRuntime] cron load failed: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(parent)
	teamEvents := make(chan []team.Message, 1)
	var wg sync.WaitGroup
	if a.team != nil {
		wg.Add(1)
		go a.watchTeamEvents(ctx, &wg, teamEvents)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		pending := asyncPending{}
		defer func() {
			if a.cron != nil && len(pending.cronJobs) > 0 {
				a.cron.Restore(pending.cronJobs)
			}
		}()

		pollTicker := time.NewTicker(time.Second)
		retryTicker := time.NewTicker(250 * time.Millisecond)
		defer pollTicker.Stop()
		defer retryTicker.Stop()
		backgroundReady := a.session.BackgroundReady()

		for {
			select {
			case <-ctx.Done():
				return
			case messages := <-teamEvents:
				pending.teamEvents = append(pending.teamEvents, messages...)
			case <-backgroundReady:
				pending.background = true
			case moment := <-pollTicker.C:
				if a.cron != nil {
					a.cron.Poll(moment)
				}
			case <-retryTicker.C:
			}

			a.collectAsyncPending(&pending)
			a.deliverAsyncPending(ctx, &pending)
		}
	}()

	return func() {
		cancel()
		wg.Wait()
	}
}

func (a *App) watchTeamEvents(ctx context.Context, wg *sync.WaitGroup, output chan<- []team.Message) {
	defer wg.Done()
	for {
		messages, err := a.team.WaitLeadEvents(ctx, time.Second)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Error("[AsyncRuntime] Lead inbox failed: %v", err)
			continue
		}
		if len(messages) == 0 {
			continue
		}
		select {
		case output <- messages:
		case <-ctx.Done():
			return
		}
	}
}

func (a *App) collectAsyncPending(pending *asyncPending) {
	if len(pending.cronJobs) == 0 && a.cron != nil && a.cron.HasPending() {
		pending.cronJobs = a.cron.Consume()
	}
	if a.session.HasBackgroundResults() {
		pending.background = true
	} else if pending.background {
		// 前台 Agent 可能已在当前回合收集结果，此时不应再触发空回合。
		pending.background = false
	}
}

func (a *App) deliverAsyncPending(ctx context.Context, pending *asyncPending) {
	if len(pending.teamEvents) == 0 && len(pending.cronJobs) == 0 && !pending.background {
		return
	}

	inputs := make([]protocol.Message, 0, len(pending.cronJobs)+1)
	for _, job := range pending.cronJobs {
		inputs = append(inputs, protocol.Message{Role: protocol.RoleUser, Content: "[Scheduled] " + job.Prompt})
	}
	if content := team.FormatEvents(pending.teamEvents); content != "" {
		inputs = append(inputs, protocol.Message{Role: protocol.RoleUser, Content: content})
	}

	// 自动回合不得竞争终端输入；需要人工审批的工具会 fail closed。
	autoContext := permission.WithInteractive(ctx, false)
	result, acquired, err := a.session.TrySubmit(autoContext, inputs...)
	if !acquired {
		logger.Debug("[AsyncRuntime] Agent busy, pending cron=%d team=%d background=%t", len(pending.cronJobs), len(pending.teamEvents), pending.background)
		return
	}
	if err != nil {
		pending.background = false
		logger.Error("[AsyncRuntime] automatic turn failed: %v", err)
		return
	}

	for _, job := range pending.cronJobs {
		logger.Info("[AsyncRuntime] delivered cron %s: %s", job.ID, previewText(job.Prompt, 60))
	}
	if a.cron != nil && len(pending.cronJobs) > 0 {
		if err := a.cron.Acknowledge(pending.cronJobs); err != nil {
			logger.Error("[AsyncRuntime] cron acknowledgement failed: %v", err)
		}
	}
	if len(pending.teamEvents) > 0 {
		logger.Info("[AsyncRuntime] delivered %d Team event(s)", len(pending.teamEvents))
	}
	if pending.background {
		logger.Info("[AsyncRuntime] delivered background task result(s)")
	}
	*pending = asyncPending{}
	printAsyncResult(a.out, result)
}

func printAsyncResult(output interface{ Write([]byte) (int, error) }, result agent.RunResult) {
	if output != nil && result.Output != "" {
		fmt.Fprintln(output, result.Output)
	}
}
