package hooks

import (
	"context"
	"errors"
	"testing"
)

func TestTriggerStopReturnsFirstNonAllowDecision(t *testing.T) {
	manager := NewManager()
	calls := 0
	manager.OnStop(func(context.Context, StopContext) (StopDecision, error) {
		calls++
		return StopDecision{Action: StopAllow}, nil
	})
	manager.OnStop(func(_ context.Context, input StopContext) (StopDecision, error) {
		calls++
		if input.TotalTokens != 7 {
			t.Fatalf("unexpected Stop context: %#v", input)
		}
		return StopDecision{Action: StopBlock, Reason: "continue"}, nil
	})
	manager.OnStop(func(context.Context, StopContext) (StopDecision, error) {
		calls++
		return StopDecision{Action: StopFailed}, nil
	})

	decision, err := manager.TriggerStop(context.Background(), StopContext{TotalTokens: 7})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != StopBlock || decision.Reason != "continue" || calls != 2 {
		t.Fatalf("unexpected decision=%#v calls=%d", decision, calls)
	}
}

func TestTriggerStopPropagatesHookError(t *testing.T) {
	manager := NewManager()
	want := errors.New("evaluate failed")
	manager.OnStop(func(context.Context, StopContext) (StopDecision, error) {
		return StopDecision{}, want
	})

	if _, err := manager.TriggerStop(context.Background(), StopContext{}); !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}
