package agent

import (
	"context"
	"errors"
	"testing"

	"go-agent-harness/internal/protocol"
)

type fakeSessionRunner struct {
	result RunResult
	err    error
}

func (r *fakeSessionRunner) Run(_ context.Context, messages []protocol.Message) (RunResult, error) {
	if r.err != nil {
		return RunResult{}, r.err
	}
	r.result.Messages = CloneMessages(messages)
	return r.result, nil
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
