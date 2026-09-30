package goal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/protocol"
)

type modelQueue struct {
	mu        sync.Mutex
	responses []agent.ModelResponse
	requests  []agent.ModelRequest
}

func (m *modelQueue) Complete(_ context.Context, request agent.ModelRequest) (agent.ModelResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, request)
	if len(m.responses) == 0 {
		return agent.ModelResponse{}, errors.New("unexpected model call")
	}
	response := m.responses[0]
	m.responses = m.responses[1:]
	return response, nil
}

func (m *modelQueue) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func TestGoalLoopContinuesInSameRunnerAndExcludesEvaluatorUsage(t *testing.T) {
	withTemporaryWorkingDirectory(t)
	mainModel := &modelQueue{responses: []agent.ModelResponse{
		assistantResponse("premature", 4, 1),
		assistantResponse("verified complete", 5, 2),
		assistantResponse("[]", 90, 10), // Memory 提取不属于主 Agent token。
	}}
	evaluatorModel := &modelQueue{responses: []agent.ModelResponse{
		assistantResponse(`{"ok":false,"reason":"missing test evidence","impossible":false}`, 100, 10),
		assistantResponse(`{"ok":true,"reason":"tests passed","impossible":false}`, 100, 10),
	}}
	controller := newIntegratedController(t, evaluatorModel, nil, 8)
	hookManager := hooks.NewManager()
	hookManager.OnStop(controller.Stop)
	runner := agent.NewRunner(mainModel, agent.NewRegistry(), hookManager, "system")
	session := agent.NewSession(runner)
	defer session.Close()
	if _, err := controller.Set("tests pass", 0); err != nil {
		t.Fatal(err)
	}
	controller.BeginQuery()

	result, err := session.Submit(context.Background(), protocol.Message{Role: protocol.RoleUser, Content: "run tests"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "verified complete" || result.Stop.Action != hooks.StopAchieved {
		t.Fatalf("unexpected Goal result: %#v", result)
	}
	if mainModel.requestCount() != 3 || evaluatorModel.requestCount() != 2 {
		t.Fatalf("unexpected calls: main=%d evaluator=%d", mainModel.requestCount(), evaluatorModel.requestCount())
	}
	if result.Usage.Total() != 12 || session.TotalTokens() != 12 {
		t.Fatalf("evaluator or Memory usage leaked into main usage: run=%#v session=%d", result.Usage, session.TotalTokens())
	}
	if strings.Contains(fmt.Sprintf("%#v", result.Messages), `{"ok"`) {
		t.Fatalf("evaluator response leaked into main history: %#v", result.Messages)
	}
	if _, active := controller.Active(); active {
		t.Fatal("achieved Goal should be cleared")
	}
}

func TestDeferredGoalResumesWhenRuntimeResultArrives(t *testing.T) {
	withTemporaryWorkingDirectory(t)
	mainModel := &modelQueue{responses: []agent.ModelResponse{
		assistantResponse("background launched", 1, 1),
		assistantResponse("[]", 50, 5),
		assistantResponse("background result verified", 2, 1),
		assistantResponse("[]", 50, 5),
	}}
	evaluatorModel := &modelQueue{responses: []agent.ModelResponse{
		assistantResponse(`{"ok":true,"reason":"result proves completion","impossible":false}`, 100, 10),
	}}
	var pending atomic.Bool
	pending.Store(true)
	controller := newIntegratedController(t, evaluatorModel, func() string {
		if pending.Load() {
			return "background work is still running"
		}
		return ""
	}, 8)
	hookManager := hooks.NewManager()
	hookManager.OnStop(controller.Stop)
	runner := agent.NewRunner(mainModel, agent.NewRegistry(), hookManager, "system")
	session := agent.NewSession(runner)
	defer session.Close()
	if _, err := controller.Set("background check passes", 0); err != nil {
		t.Fatal(err)
	}
	controller.BeginQuery()

	first, err := session.Submit(context.Background(), protocol.Message{Role: protocol.RoleUser, Content: "start check"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Stop.Action != hooks.StopDefer || evaluatorModel.requestCount() != 0 {
		t.Fatalf("running work should defer without evaluation: %#v", first.Stop)
	}

	pending.Store(false)
	controller.BeginQuery()
	second, acquired, err := session.TrySubmit(context.Background(), protocol.Message{
		Role: protocol.RoleUser, Content: "<task_notification>check passed</task_notification>",
	})
	if err != nil || !acquired {
		t.Fatalf("runtime result was not submitted: acquired=%v err=%v", acquired, err)
	}
	if second.Stop.Action != hooks.StopAchieved || evaluatorModel.requestCount() != 1 {
		t.Fatalf("runtime result did not resume Goal evaluation: %#v", second.Stop)
	}
	if session.TotalTokens() != 5 {
		t.Fatalf("unexpected main Agent tokens: %d", session.TotalTokens())
	}
}

func TestGlobalTurnLimitLeavesGoalActive(t *testing.T) {
	withTemporaryWorkingDirectory(t)
	mainModel := &modelQueue{responses: []agent.ModelResponse{assistantResponse("not done", 1, 1)}}
	evaluatorModel := &modelQueue{responses: []agent.ModelResponse{
		assistantResponse(`{"ok":false,"reason":"evidence missing","impossible":false}`, 10, 2),
	}}
	controller := newIntegratedController(t, evaluatorModel, nil, 8)
	hookManager := hooks.NewManager()
	hookManager.OnStop(controller.Stop)
	runner := agent.NewRunner(mainModel, agent.NewRegistry(), hookManager, "system", agent.WithMaxTurns(1))
	defer runner.Close()
	if _, err := controller.Set("prove completion", 0); err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), []protocol.Message{{Role: protocol.RoleUser, Content: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stop.Action != hooks.StopLimit || !strings.Contains(result.Stop.Reason, "max_turns") {
		t.Fatalf("unexpected limit result: %#v", result.Stop)
	}
	if state, active := controller.Active(); !active || state.Iterations != 1 {
		t.Fatalf("turn limit cleared or corrupted Goal: %#v active=%v", state, active)
	}
}

func newIntegratedController(t *testing.T, evaluatorModel agent.Model, pending func() string, blockCap int) *Controller {
	t.Helper()
	evaluator, err := NewPromptEvaluator(evaluatorModel, "judge", 0)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := New(Config{Evaluator: evaluator, PendingReason: pending, BlockCap: blockCap})
	if err != nil {
		t.Fatal(err)
	}
	return controller
}

func assistantResponse(text string, inputTokens, outputTokens int64) agent.ModelResponse {
	return agent.ModelResponse{
		Message: protocol.Message{Role: protocol.RoleAssistant, Content: text},
		Usage:   agent.TokenUsage{InputTokens: inputTokens, OutputTokens: outputTokens},
	}
}

func withTemporaryWorkingDirectory(t *testing.T) {
	t.Helper()
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })
}
