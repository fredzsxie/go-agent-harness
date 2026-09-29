package agent

import (
	"context"
	"errors"
	"testing"

	"go-agent-harness/internal/protocol"
)

type fakeSessionRunner struct {
	result    RunResult
	err       error
	requests  []string
	baselines []int64
}

func (r *fakeSessionRunner) Run(ctx context.Context, messages []protocol.Message) (RunResult, error) {
	request, _ := activeRequestFromContext(ctx)
	r.requests = append(r.requests, request)
	r.baselines = append(r.baselines, tokenBaselineFromContext(ctx))
	if r.err != nil {
		return RunResult{}, r.err
	}
	r.result.Messages = CloneMessages(messages)
	return r.result, nil
}

func TestSessionKeepsUserRequestAcrossAutomaticTurns(t *testing.T) {
	runner := &fakeSessionRunner{}
	session := newSession(runner)
	if _, err := session.Submit(context.Background(), protocol.Message{Role: protocol.RoleUser, Content: "user task"}); err != nil {
		t.Fatal(err)
	}
	if _, acquired, err := session.TrySubmit(context.Background()); err != nil || !acquired {
		t.Fatalf("background turn failed: acquired=%v err=%v", acquired, err)
	}
	scheduled := WithActiveRequest(context.Background(), "Run scheduled task: check")
	if _, acquired, err := session.TrySubmit(scheduled, protocol.Message{Role: protocol.RoleUser, Content: "[Scheduled] check"}); err != nil || !acquired {
		t.Fatalf("scheduled turn failed: acquired=%v err=%v", acquired, err)
	}
	if _, acquired, err := session.TrySubmit(context.Background()); err != nil || !acquired {
		t.Fatalf("second background turn failed: acquired=%v err=%v", acquired, err)
	}
	want := []string{"user task", "user task", "Run scheduled task: check", "user task"}
	if len(runner.requests) != len(want) {
		t.Fatalf("unexpected requests: %#v", runner.requests)
	}
	for i := range want {
		if runner.requests[i] != want[i] {
			t.Fatalf("request %d = %q, want %q", i, runner.requests[i], want[i])
		}
	}
}

func (*fakeSessionRunner) Close() {}

func TestSessionCommitsMessagesOnlyAfterSuccessfulRun(t *testing.T) {
	runner := &fakeSessionRunner{}
	session := newSession(runner)
	if _, err := session.Submit(context.Background(), protocol.Message{Role: protocol.RoleUser, Content: "first"}); err != nil {
		t.Fatal(err)
	}
	runner.err = errors.New("failed")
	if _, err := session.Submit(context.Background(), protocol.Message{Role: protocol.RoleUser, Content: "discarded"}); err == nil {
		t.Fatal("expected run error")
	}
	runner.err = nil
	result, err := session.Submit(context.Background(), protocol.Message{Role: protocol.RoleUser, Content: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 2 || result.Messages[0].Content != "first" || result.Messages[1].Content != "second" {
		t.Fatalf("unexpected messages: %#v", result.Messages)
	}
}

func TestSessionAccumulatesMainAgentUsage(t *testing.T) {
	runner := &fakeSessionRunner{result: RunResult{Usage: TokenUsage{InputTokens: 8, OutputTokens: 3}}}
	session := newSession(runner)
	if _, err := session.Submit(context.Background(), protocol.Message{Role: protocol.RoleUser, Content: "first"}); err != nil {
		t.Fatal(err)
	}
	runner.result.Usage = TokenUsage{InputTokens: 5, OutputTokens: 2}
	if _, err := session.Submit(context.Background(), protocol.Message{Role: protocol.RoleUser, Content: "second"}); err != nil {
		t.Fatal(err)
	}
	if got := session.TotalTokens(); got != 18 {
		t.Fatalf("TotalTokens() = %d, want 18", got)
	}
	if len(runner.baselines) != 2 || runner.baselines[0] != 0 || runner.baselines[1] != 11 {
		t.Fatalf("unexpected token baselines: %#v", runner.baselines)
	}
}
