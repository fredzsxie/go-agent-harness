package app

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/protocol"
	"go-agent-harness/internal/team"
)

type recordingSession struct {
	mu       sync.Mutex
	attempts int
	inputs   []protocol.Message
	busyOnce bool
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (s *recordingSession) Submit(_ context.Context, inputs ...protocol.Message) (agent.RunResult, error) {
	s.mu.Lock()
	s.inputs = append(s.inputs, inputs...)
	s.mu.Unlock()
	return agent.RunResult{}, nil
}

func (s *recordingSession) TrySubmit(_ context.Context, before, after func(), inputs ...protocol.Message) (agent.RunResult, bool, error) {
	s.mu.Lock()
	s.attempts++
	busy := s.busyOnce && s.attempts == 1
	s.mu.Unlock()
	if busy {
		return agent.RunResult{}, false, nil
	}
	if before != nil {
		before()
	}
	if after != nil {
		defer after()
	}
	s.mu.Lock()
	s.inputs = append(s.inputs, inputs...)
	s.mu.Unlock()
	return agent.RunResult{Output: "team event handled"}, true, nil
}

func (s *recordingSession) Close() {}

func TestTeamEventsRetryBusySessionAndStartLeadTurn(t *testing.T) {
	root := t.TempDir()
	bus := team.NewBus(team.BusConfig{WorkDir: root})
	runtime := team.NewRuntime(team.RuntimeConfig{Bus: bus, Requests: team.NewRequests()})
	session := &recordingSession{busyOnce: true}
	mode := &atomic.Bool{}
	output := &lockedBuffer{}
	application := &App{session: session, team: runtime, nonInteractive: mode, out: output}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := application.startTeamEventRuntime(ctx)
	defer stop()
	if _, err := bus.Send("alice", "lead", "implementation done", team.MessageResult, team.Metadata{}); err != nil {
		t.Fatal(err)
	}

	waitUntil(t, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return len(session.inputs) == 1 && strings.Contains(output.String(), "team event handled")
	})
	session.mu.Lock()
	attempts := session.attempts
	input := session.inputs[0]
	session.mu.Unlock()
	if attempts < 2 {
		t.Fatalf("busy Session should be retried, attempts=%d", attempts)
	}
	if input.Role != protocol.RoleUser || !strings.Contains(input.Content, "[result] alice: implementation done") {
		t.Fatalf("unexpected Team event input: %#v", input)
	}
	if mode.Load() {
		t.Fatal("non-interactive mode should be restored after delivery")
	}
	if !strings.Contains(output.String(), "team event handled") {
		t.Fatalf("Lead output was not printed: %q", output.String())
	}
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied")
}
