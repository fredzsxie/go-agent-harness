package goal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/protocol"
)

type evaluatorReply struct {
	evaluation Evaluation
	err        error
}

type sequenceEvaluator struct {
	replies    []evaluatorReply
	conditions []string
}

func (e *sequenceEvaluator) Evaluate(_ context.Context, condition string, _ []protocol.Message) (Evaluation, error) {
	e.conditions = append(e.conditions, condition)
	if len(e.replies) == 0 {
		return Evaluation{}, errors.New("unexpected evaluation")
	}
	reply := e.replies[0]
	e.replies = e.replies[1:]
	return reply.evaluation, reply.err
}

func TestControllerLifecycleAndStatus(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.Local)
	controller, err := New(Config{Evaluator: &sequenceEvaluator{}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Set("  tests pass  ", 100); err != nil {
		t.Fatal(err)
	}
	now = now.Add(5 * time.Second)
	status := controller.Status(125)
	for _, want := range []string{"Goal active: tests pass", "Elapsed: 5s", "Evaluations: 0", "Tokens: 25"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status missing %q:\n%s", want, status)
		}
	}
	if _, err := controller.Set("lint passes", 125); err != nil {
		t.Fatal(err)
	}
	if events := controller.Events(); len(events) != 3 || events[1].Reason != "replaced by a new goal" {
		t.Fatalf("unexpected replacement events: %#v", events)
	}
	cleared, ok := controller.Clear("user canceled")
	if !ok || cleared.Condition != "lint passes" || controller.Status(130) != "No goal set" {
		t.Fatalf("unexpected clear result: state=%#v ok=%v status=%q", cleared, ok, controller.Status(130))
	}
}

func TestControllerEvaluatesBlockAndAchievement(t *testing.T) {
	evaluator := &sequenceEvaluator{replies: []evaluatorReply{
		{evaluation: Evaluation{Reason: "missing test output"}},
		{evaluation: Evaluation{OK: true, Reason: "tests passed"}},
	}}
	controller, err := New(Config{Evaluator: evaluator})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Set("all tests pass", 0); err != nil {
		t.Fatal(err)
	}
	messages := []protocol.Message{{Role: protocol.RoleAssistant, Content: "working"}}
	if decision := controller.EvaluateAfterTurn(context.Background(), messages, false); decision.Action != hooks.StopBlock || decision.Reason != "missing test output" {
		t.Fatalf("unexpected first decision: %#v", decision)
	}
	state, ok := controller.Active()
	if !ok || state.Iterations != 1 || state.LastReason != "missing test output" {
		t.Fatalf("unexpected active state: %#v ok=%v", state, ok)
	}
	if decision := controller.EvaluateAfterTurn(context.Background(), messages, false); decision.Action != hooks.StopAchieved {
		t.Fatalf("unexpected achieved decision: %#v", decision)
	}
	if _, ok := controller.Active(); ok || !strings.Contains(controller.Status(0), "Goal achieved") {
		t.Fatalf("achieved goal should be inactive: %s", controller.Status(0))
	}
}

func TestControllerStopBuildsSameLoopContinuation(t *testing.T) {
	controller, err := New(Config{Evaluator: &sequenceEvaluator{replies: []evaluatorReply{{
		evaluation: Evaluation{Reason: "test evidence is missing"},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Set("go test ./... passes", 0); err != nil {
		t.Fatal(err)
	}
	decision, err := controller.Stop(context.Background(), hooks.StopContext{
		Messages: []protocol.Message{{Role: protocol.RoleAssistant, Content: "finished"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != hooks.StopBlock {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	for _, want := range []string{"[Goal still active]", "Condition: go test ./... passes", "Evaluator: test evidence is missing", "Continue working"} {
		if !strings.Contains(decision.Reason, want) {
			t.Fatalf("continuation missing %q:\n%s", want, decision.Reason)
		}
	}
}

func TestControllerDefersAndStopsAtBlockCap(t *testing.T) {
	evaluator := &sequenceEvaluator{replies: []evaluatorReply{
		{evaluation: Evaluation{Reason: "first"}},
		{evaluation: Evaluation{Reason: "second"}},
	}}
	controller, err := New(Config{Evaluator: evaluator, BlockCap: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Set("done", 0); err != nil {
		t.Fatal(err)
	}
	if decision := controller.EvaluateAfterTurn(context.Background(), nil, true); decision.Action != hooks.StopDefer || len(evaluator.conditions) != 0 {
		t.Fatalf("pending work should skip evaluator: %#v", decision)
	}
	if decision := controller.EvaluateAfterTurn(context.Background(), nil, false); decision.Action != hooks.StopBlock {
		t.Fatalf("first incomplete result should block: %#v", decision)
	}
	if decision := controller.EvaluateAfterTurn(context.Background(), nil, false); decision.Action != hooks.StopLimit {
		t.Fatalf("second incomplete result should hit cap: %#v", decision)
	}
	if _, ok := controller.Active(); !ok {
		t.Fatal("limit must leave the goal active")
	}
}

func TestControllerStopDefersForPendingRuntimeWork(t *testing.T) {
	evaluator := &sequenceEvaluator{}
	controller, err := New(Config{
		Evaluator: evaluator,
		PendingReason: func() string {
			return `teammate "alice" is waiting for approval`
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Set("finish implementation", 0); err != nil {
		t.Fatal(err)
	}
	decision, err := controller.Stop(context.Background(), hooks.StopContext{})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != hooks.StopDefer || !strings.Contains(decision.Reason, "waiting for approval") || len(evaluator.conditions) != 0 {
		t.Fatalf("pending work should return control without evaluation: %#v", decision)
	}
	if _, ok := controller.Active(); !ok {
		t.Fatal("deferred Goal must remain active")
	}
}

func TestControllerKeepsGoalActiveOnEvaluatorError(t *testing.T) {
	controller, err := New(Config{Evaluator: &sequenceEvaluator{replies: []evaluatorReply{{err: errors.New("offline")}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Set("done", 0); err != nil {
		t.Fatal(err)
	}
	decision := controller.EvaluateAfterTurn(context.Background(), nil, false)
	if decision.Action != hooks.StopError || !strings.Contains(decision.Reason, "offline") {
		t.Fatalf("unexpected error decision: %#v", decision)
	}
	if state, ok := controller.Active(); !ok || !strings.Contains(state.LastReason, "offline") {
		t.Fatalf("evaluator error must keep active state: %#v ok=%v", state, ok)
	}
}

func TestRestoreOnlyRestartsActiveGoal(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.Local)
	active, err := Restore(Config{Now: func() time.Time { return now }}, []Event{
		{Type: "goal_status", Condition: "old", Met: true},
		{Type: "goal_status", Condition: "current", Active: true, Iterations: 7},
	}, 42)
	if err != nil {
		t.Fatal(err)
	}
	state, ok := active.Active()
	if !ok || state.Condition != "current" || state.Iterations != 0 || state.TokensAtStart != 42 || !state.SetAt.Equal(now) {
		t.Fatalf("unexpected restored state: %#v ok=%v", state, ok)
	}

	completed, err := Restore(Config{}, []Event{{Type: "goal_status", Condition: "done", Met: true}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := completed.Active(); ok || !strings.Contains(completed.Status(0), "Goal achieved") {
		t.Fatalf("completed goal should not restart: %s", completed.Status(0))
	}
}

type blockingEvaluator struct {
	started chan struct{}
	release chan struct{}
}

func (e *blockingEvaluator) Evaluate(context.Context, string, []protocol.Message) (Evaluation, error) {
	close(e.started)
	<-e.release
	return Evaluation{OK: true, Reason: "stale success"}, nil
}

func TestControllerIgnoresStaleEvaluationAfterReplacement(t *testing.T) {
	evaluator := &blockingEvaluator{started: make(chan struct{}), release: make(chan struct{})}
	controller, err := New(Config{Evaluator: evaluator})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Set("old goal", 0); err != nil {
		t.Fatal(err)
	}
	decision := make(chan hooks.StopDecision, 1)
	go func() { decision <- controller.EvaluateAfterTurn(context.Background(), nil, false) }()
	<-evaluator.started
	if _, err := controller.Set("new goal", 0); err != nil {
		t.Fatal(err)
	}
	close(evaluator.release)
	if got := <-decision; got.Action != hooks.StopBlock {
		t.Fatalf("stale result should continue under replacement goal: %#v", got)
	}
	state, ok := controller.Active()
	if !ok || state.Condition != "new goal" {
		t.Fatalf("stale result replaced current state: %#v ok=%v", state, ok)
	}
}
