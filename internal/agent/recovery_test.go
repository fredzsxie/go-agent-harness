package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go-agent-harness/internal/hooks"
	"go-agent-harness/internal/protocol"
)

type scriptedModelResult struct {
	response ModelResponse
	err      error
}

type scriptedModel struct {
	mu       sync.Mutex
	results  []scriptedModelResult
	requests []ModelRequest
}

func (m *scriptedModel) Complete(_ context.Context, request ModelRequest) (ModelResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, request)
	if len(m.results) == 0 {
		return ModelResponse{}, errors.New("unexpected model call")
	}
	result := m.results[0]
	m.results = m.results[1:]
	return result.response, result.err
}

func TestRunTurnWithRetrySwitchesModelAfterConsecutive529(t *testing.T) {
	model := &scriptedModel{results: []scriptedModelResult{
		{err: &ModelError{HTTPStatus: 529, Err: errors.New("overloaded")}},
		{err: &ModelError{HTTPStatus: 529, Err: errors.New("overloaded")}},
		{response: ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "ok"}}},
	}}
	runner := NewRunner(model, NewRegistry(), nil, "system", WithFallbackModel("fallback-model"))
	defer runner.Close()
	runner.recovery.sleep = func(context.Context, time.Duration) error { return nil }
	runner.recovery.jitter = func(time.Duration) time.Duration { return 0 }

	state := recoveryState{fallbackModel: runner.fallbackModel}
	turn, err := runner.runTurnWithRetry(context.Background(), &state, "system", nil, DefaultMaxTokens, nil)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Assistant.Content != "ok" {
		t.Fatalf("unexpected response: %#v", turn)
	}
	models := []string{model.requests[0].Model, model.requests[1].Model, model.requests[2].Model}
	if models[0] != "" || models[1] != "" || models[2] != "fallback-model" {
		t.Fatalf("unexpected model sequence: %#v", models)
	}
}

func TestRunTurnWithRetryKeepsPrimaryModelFor429(t *testing.T) {
	model := &scriptedModel{results: []scriptedModelResult{
		{err: &ModelError{HTTPStatus: 429, Err: errors.New("rate limited")}},
		{err: &ModelError{HTTPStatus: 429, Err: errors.New("rate limited")}},
		{response: ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "ok"}}},
	}}
	runner := NewRunner(model, NewRegistry(), nil, "system", WithFallbackModel("fallback-model"))
	defer runner.Close()
	runner.recovery.sleep = func(context.Context, time.Duration) error { return nil }
	runner.recovery.jitter = func(time.Duration) time.Duration { return 0 }

	state := recoveryState{fallbackModel: runner.fallbackModel}
	if _, err := runner.runTurnWithRetry(context.Background(), &state, "system", nil, DefaultMaxTokens, nil); err != nil {
		t.Fatal(err)
	}
	if len(model.requests) != 3 {
		t.Fatalf("expected three model attempts, got %d", len(model.requests))
	}
	for _, request := range model.requests {
		if request.Model != "" {
			t.Fatalf("429 should not switch the primary model: %#v", model.requests)
		}
	}
}

func TestRunTurnWithRetryDoesNotRetryCanceledContext(t *testing.T) {
	model := &scriptedModel{results: []scriptedModelResult{{
		err: &ModelError{HTTPStatus: 429, Err: context.Canceled},
	}}}
	runner := NewRunner(model, NewRegistry(), nil, "system")
	defer runner.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	state := recoveryState{}
	if _, err := runner.runTurnWithRetry(ctx, &state, "system", nil, DefaultMaxTokens, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if len(model.requests) != 1 {
		t.Fatalf("canceled context should not retry, calls=%d", len(model.requests))
	}
}

func TestRunnerEscalatesAndContinuesTruncatedResponse(t *testing.T) {
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })
	model := &scriptedModel{results: []scriptedModelResult{
		{response: ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "discarded"}, StopReason: "max_tokens", Usage: TokenUsage{InputTokens: 10, OutputTokens: 1}}},
		{response: ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "part one"}, StopReason: "max_tokens", Usage: TokenUsage{InputTokens: 11, OutputTokens: 2}}},
		{response: ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "part two"}, StopReason: "end_turn", Usage: TokenUsage{InputTokens: 12, OutputTokens: 3}}},
		{response: ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "[]"}, StopReason: "end_turn"}},
	}}
	runner := NewRunner(model, NewRegistry(), nil, "system")
	defer runner.Close()

	result, err := runner.Run(context.Background(), []protocol.Message{{Role: protocol.RoleUser, Content: "write"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "part two" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	if result.Usage != (TokenUsage{InputTokens: 33, OutputTokens: 6}) {
		t.Fatalf("unexpected main Agent usage: %#v", result.Usage)
	}
	if len(model.requests) < 3 || model.requests[0].MaxTokens != DefaultMaxTokens || model.requests[1].MaxTokens != escalatedMaxTokens {
		t.Fatalf("unexpected token escalation: %#v", model.requests)
	}
	thirdMessages := model.requests[2].Messages
	if len(thirdMessages) < 3 || thirdMessages[len(thirdMessages)-2].Content != "part one" || thirdMessages[len(thirdMessages)-1].Content != continuationPrompt {
		t.Fatalf("continuation context missing: %#v", thirdMessages)
	}
}

func TestRunnerContinuesOnlyWhenStopHookBlocks(t *testing.T) {
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })

	model := &scriptedModel{results: []scriptedModelResult{
		{response: ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "not yet"}, Usage: TokenUsage{InputTokens: 4, OutputTokens: 1}}},
		{response: ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "done"}, Usage: TokenUsage{InputTokens: 6, OutputTokens: 2}}},
		{response: ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "[]"}}},
	}}
	hookManager := hooks.NewManager()
	stopCalls := 0
	hookManager.OnStop(func(_ context.Context, input hooks.StopContext) (hooks.StopDecision, error) {
		stopCalls++
		if len(input.Messages) == 0 || input.Messages[len(input.Messages)-1].Role != protocol.RoleAssistant {
			t.Fatalf("Stop hook did not receive the completed turn: %#v", input.Messages)
		}
		if stopCalls == 1 {
			return hooks.StopDecision{Action: hooks.StopBlock, Reason: "[Goal still active] continue"}, nil
		}
		return hooks.StopDecision{Action: hooks.StopAllow}, nil
	})
	runner := NewRunner(model, NewRegistry(), hookManager, "system")
	defer runner.Close()

	result, err := runner.Run(context.Background(), []protocol.Message{{Role: protocol.RoleUser, Content: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" || result.Stop.Action != hooks.StopAllow || stopCalls != 2 {
		t.Fatalf("unexpected result=%#v stopCalls=%d", result, stopCalls)
	}
	if result.Usage != (TokenUsage{InputTokens: 10, OutputTokens: 3}) {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
	secondRequest := model.requests[1].Messages
	if len(secondRequest) == 0 || secondRequest[len(secondRequest)-1].Content != "[Goal still active] continue" {
		t.Fatalf("Stop feedback was not appended: %#v", secondRequest)
	}
}

func TestRunnerReturnsControlAtGlobalTurnLimit(t *testing.T) {
	oldDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDir) })

	model := &scriptedModel{results: []scriptedModelResult{
		{response: ModelResponse{
			Message: protocol.Message{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{
				Type: protocol.BlockToolUse, ToolUseID: "tool_1", ToolName: "echo", Input: map[string]any{"text": "work"},
			}}},
			Usage: TokenUsage{InputTokens: 3, OutputTokens: 2},
		}},
	}}
	registry := NewRegistry()
	registry.Register(ToolSpec{Name: "echo"}, func(context.Context, any) (string, error) { return "done", nil })
	runner := NewRunner(model, registry, nil, "system", WithMaxTurns(1))
	defer runner.Close()

	result, err := runner.Run(context.Background(), []protocol.Message{{Role: protocol.RoleUser, Content: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stop.Action != hooks.StopLimit || !strings.Contains(result.Stop.Reason, "max_turns") || len(model.requests) != 1 {
		t.Fatalf("unexpected limited result=%#v requests=%d", result, len(model.requests))
	}
	if result.Usage.Total() != 5 {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
}
