package agent

import (
	"context"
	"errors"
	"sync"

	"go-agent-harness/internal/protocol"
)

type sessionRunner interface {
	Run(context.Context, []protocol.Message) (RunResult, error)
	Close()
}

// Session 持有单个 Agent 的消息历史，并串行执行用户与 Cron 输入。
type Session struct {
	mu       sync.Mutex
	runner   sessionRunner
	messages []protocol.Message
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
	return s.run(ctx, inputs)
}

// TrySubmit 供 Cron 非阻塞投递；false 表示 Session 当前仍在处理其他输入。
func (s *Session) TrySubmit(ctx context.Context, before, after func(), inputs ...protocol.Message) (RunResult, bool, error) {
	if !s.mu.TryLock() {
		return RunResult{}, false, nil
	}
	defer s.mu.Unlock()
	if before != nil {
		before()
	}
	if after != nil {
		defer after()
	}
	result, err := s.run(ctx, inputs)
	return result, true, err
}

func (s *Session) run(ctx context.Context, inputs []protocol.Message) (RunResult, error) {
	if s.runner == nil {
		return RunResult{}, errors.New("session runner is not configured")
	}
	candidate := append(CloneMessages(s.messages), inputs...)
	result, err := s.runner.Run(ctx, candidate)
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
