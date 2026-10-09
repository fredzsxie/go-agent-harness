package goal

import (
	"context"
	"strings"
	"testing"

	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/protocol"
)

type captureModel struct {
	request  llm.Request
	response llm.Response
	err      error
}

func (m *captureModel) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	m.request = request
	return m.response, m.err
}

func TestPromptEvaluatorUsesToolFreeIndependentRequest(t *testing.T) {
	model := &captureModel{response: llm.Response{Message: protocol.Message{
		Role:   protocol.RoleAssistant,
		Blocks: []protocol.ContentBlock{{Type: protocol.BlockText, Text: `{"ok":true,"reason":"tests passed","impossible":false}`}},
	}}}
	evaluator, err := NewPromptEvaluator(model, "evaluator-model", 321)
	if err != nil {
		t.Fatal(err)
	}
	result, err := evaluator.Evaluate(context.Background(), "tests pass", []protocol.Message{{Role: protocol.RoleUser, Content: "run tests"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.Reason != "tests passed" || result.Impossible {
		t.Fatalf("unexpected evaluation: %#v", result)
	}
	request := model.request
	if request.Model != "evaluator-model" || request.MaxTokens != 321 || request.System != evaluatorSystemPrompt || len(request.Tools) != 0 {
		t.Fatalf("unexpected evaluator request: %#v", request)
	}
	if len(request.Messages) != 1 || !strings.Contains(request.Messages[0].Content, `"completion_condition":"tests pass"`) {
		t.Fatalf("evaluation payload missing: %#v", request.Messages)
	}
}

func TestParseEvaluationValidatesExactContract(t *testing.T) {
	validFalse := false
	tests := []struct {
		name    string
		value   string
		want    Evaluation
		wantErr string
	}{
		{name: "valid", value: `{"ok":false,"reason":" missing evidence ","impossible":false}`, want: Evaluation{OK: false, Reason: "missing evidence", Impossible: validFalse}},
		{name: "fenced", value: "```json\n{\"ok\":true,\"reason\":\"done\"}\n```", want: Evaluation{OK: true, Reason: "done"}},
		{name: "missing ok", value: `{"reason":"no"}`, wantErr: "requires boolean 'ok'"},
		{name: "empty reason", value: `{"ok":false,"reason":" "}`, wantErr: "non-empty 'reason'"},
		{name: "conflict", value: `{"ok":true,"reason":"bad","impossible":true}`, wantErr: "both ok and impossible"},
		{name: "unknown", value: `{"ok":false,"reason":"no","extra":1}`, wantErr: "unknown field"},
		{name: "trailing", value: `{"ok":false,"reason":"no"} {}`, wantErr: "trailing JSON"},
		{name: "array", value: `[]`, wantErr: "invalid JSON"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseEvaluation(test.value)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("expected error containing %q, got %v", test.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("parseEvaluation() = %#v, want %#v", got, test.want)
			}
		})
	}
}
