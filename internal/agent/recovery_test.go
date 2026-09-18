package agent

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

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
		{response: ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "discarded"}, StopReason: "max_tokens"}},
		{response: ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "part one"}, StopReason: "max_tokens"}},
		{response: ModelResponse{Message: protocol.Message{Role: protocol.RoleAssistant, Content: "part two"}, StopReason: "end_turn"}},
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
	if len(model.requests) < 3 || model.requests[0].MaxTokens != DefaultMaxTokens || model.requests[1].MaxTokens != escalatedMaxTokens {
		t.Fatalf("unexpected token escalation: %#v", model.requests)
	}
	thirdMessages := model.requests[2].Messages
	if len(thirdMessages) < 3 || thirdMessages[len(thirdMessages)-2].Content != "part one" || thirdMessages[len(thirdMessages)-1].Content != continuationPrompt {
		t.Fatalf("continuation context missing: %#v", thirdMessages)
	}
}
