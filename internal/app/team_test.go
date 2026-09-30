package app

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/permission"
	"go-agent-harness/internal/protocol"
	"go-agent-harness/internal/team"
)

type recordingSession struct {
	mu          sync.Mutex
	attempts    int
	inputs      []protocol.Message
	busyOnce    bool
	interactive bool
	ready       chan struct{}
	background  bool
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

func (s *recordingSession) TrySubmit(ctx context.Context, inputs ...protocol.Message) (agent.RunResult, bool, error) {
	s.mu.Lock()
	s.attempts++
	busy := s.busyOnce && s.attempts == 1
	s.mu.Unlock()
	if busy {
		return agent.RunResult{}, false, nil
	}
	s.mu.Lock()
	s.inputs = append(s.inputs, inputs...)
	s.interactive = permission.IsInteractive(ctx)
	s.background = false
	s.mu.Unlock()
	return agent.RunResult{Output: "team event handled"}, true, nil
}

func (s *recordingSession) Close()                           {}
func (s *recordingSession) BackgroundReady() <-chan struct{} { return s.ready }
func (s *recordingSession) HasBackgroundResults() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.background
}

func TestTeamEventsRetryBusySessionAndStartLeadTurn(t *testing.T) {
	root := t.TempDir()
	bus := team.NewBus(team.BusConfig{WorkDir: root})
	runtime := team.NewRuntime(team.RuntimeConfig{Bus: bus, Requests: team.NewRequests()})
	session := &recordingSession{busyOnce: true}
	output := &lockedBuffer{}
	application := &App{session: session, team: runtime, out: output}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := application.startAsyncRuntime(ctx)
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
	interactive := session.interactive
	session.mu.Unlock()
	if attempts < 2 {
		t.Fatalf("busy Session should be retried, attempts=%d", attempts)
	}
	if input.Role != protocol.RoleUser || !strings.Contains(input.Content, "[result] alice: implementation done") {
		t.Fatalf("unexpected Team event input: %#v", input)
	}
	if interactive {
		t.Fatal("automatic Team turn should use non-interactive permission context")
	}
	if !strings.Contains(output.String(), "team event handled") {
		t.Fatalf("Lead output was not printed: %q", output.String())
	}
}

func TestBackgroundCompletionStartsAutomaticTurn(t *testing.T) {
	ready := make(chan struct{}, 1)
	session := &recordingSession{ready: ready, background: true}
	output := &lockedBuffer{}
	application := &App{session: session, out: output}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := application.startAsyncRuntime(ctx)
	defer stop()

	ready <- struct{}{}
	waitUntil(t, func() bool {
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.attempts == 1
	})
	session.mu.Lock()
	interactive := session.interactive
	inputs := len(session.inputs)
	session.mu.Unlock()
	if interactive || inputs != 0 {
		t.Fatalf("background wake should be non-interactive without synthetic input: interactive=%v inputs=%d", interactive, inputs)
	}
}

func TestGoalPendingReasonDistinguishesRuntimeStates(t *testing.T) {
	tests := []struct {
		name       string
		background bool
		teammates  []team.TeammateInfo
		want       string
	}{
		{name: "none"},
		{name: "background", background: true, want: "background work is still running"},
		{name: "working", teammates: []team.TeammateInfo{{Name: "alice", Status: team.TeammateWorking}}, want: `teammate "alice" is still working`},
		{name: "stopping", teammates: []team.TeammateInfo{{Name: "alice", Status: team.TeammateStopping}}, want: `teammate "alice" is still stopping`},
		{name: "idle", teammates: []team.TeammateInfo{{Name: "alice", Status: team.TeammateIdle}}},
		{name: "approval takes priority", background: true, teammates: []team.TeammateInfo{{Name: "alice", Status: team.TeammateWaitingApproval}}, want: `teammate "alice" is waiting for approval`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := goalPendingReason(test.background, test.teammates); got != test.want {
				t.Fatalf("goalPendingReason() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPrintRunResultIncludesTerminalGoalDecision(t *testing.T) {
	output := &bytes.Buffer{}
	printRunResult(output, agent.RunResult{
		Output: "main response",
		Stop:   hooks.StopDecision{Action: hooks.StopDefer, Reason: "background work is still running"},
	})
	for _, want := range []string{"main response", "[goal] defer: background work is still running"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q: %q", want, output.String())
		}
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
