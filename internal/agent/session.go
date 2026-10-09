package agent

import (
	"context"
	"errors"
	"sync"

	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/protocol"
)

type sessionRunner interface {
	Run(context.Context, []protocol.Message) (RunResult, error)
	Close()
}

type backgroundEvents interface {
	BackgroundReady() <-chan struct{}
	HasBackgroundResults() bool
	HasBackgroundWork() bool
}

// Session 是 s15 的单写入入口：用户、Cron、Team 和后台事件共用同一条消息历史。
// 自动事件使用 TrySubmit，忙时由 app 保留待投递数据，不能并行改写 Runner 的上下文。
type Session struct {
	mu            sync.Mutex
	runner        sessionRunner
	messages      []protocol.Message
	activeRequest string
	totalUsage    llm.Usage
}

func NewSession(runner *Runner) *Session {
	return newSession(runner)
}

func newSession(runner sessionRunner) *Session {
	return &Session{runner: runner, messages: make([]protocol.Message, 0, 16)}
}

// Submit 等待当前 Agent Loop 结束，然后提交一批同源输入。
func (s *Session) Submit(ctx context.Context, inputs ...protocol.Message) (RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	request := protocol.ActiveRequest(inputs)
	result, err := s.run(WithActiveRequest(ctx, request), inputs)
	if err == nil && request != "" {
		s.activeRequest = request
	}
	return result, err
}

// TrySubmit 供自动事件非阻塞投递；false 表示 Session 当前仍在处理其他输入。
func (s *Session) TrySubmit(ctx context.Context, inputs ...protocol.Message) (RunResult, bool, error) {
	if !s.mu.TryLock() {
		return RunResult{}, false, nil
	}
	defer s.mu.Unlock()
	result, err := s.run(ctx, inputs)
	return result, true, err
}

// BackgroundReady 转发 Runner 的后台完成信号。
func (s *Session) BackgroundReady() <-chan struct{} {
	if source, ok := s.runner.(backgroundEvents); ok {
		return source.BackgroundReady()
	}
	return nil
}

// HasBackgroundResults 转发 Runner 的待投递状态。
func (s *Session) HasBackgroundResults() bool {
	if source, ok := s.runner.(backgroundEvents); ok {
		return source.HasBackgroundResults()
	}
	return false
}

// HasBackgroundWork 转发 Runner 的后台运行状态；此方法不获取 Session 锁，Stop Hook 可安全调用。
func (s *Session) HasBackgroundWork() bool {
	if source, ok := s.runner.(backgroundEvents); ok {
		return source.HasBackgroundWork()
	}
	return false
}

// TotalTokens 返回当前 Session 中主 Agent 调用累计消耗的 token。
// Workflow、memory 和后续 Goal evaluator 的独立模型调用不计入该值。
func (s *Session) TotalTokens() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totalUsage.Total()
}

func (s *Session) run(ctx context.Context, inputs []protocol.Message) (RunResult, error) {
	if s.runner == nil {
		return RunResult{}, errors.New("session runner is not configured")
	}
	if _, configured := activeRequestFromContext(ctx); !configured && s.activeRequest != "" {
		ctx = WithActiveRequest(ctx, s.activeRequest)
	}
	ctx = withTokenBaseline(ctx, s.totalUsage.Total())
	candidate := append(protocol.CloneMessages(s.messages), inputs...)
	result, err := s.runner.Run(ctx, candidate)
	s.totalUsage.Add(result.Usage)
	if err == nil {
		s.messages = result.Messages
	}
	return result, err
}

func (s *Session) Close() {
	if s.runner != nil {
		s.runner.Close()
	}
}
