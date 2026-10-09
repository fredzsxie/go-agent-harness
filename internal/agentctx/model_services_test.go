package agentctx

import (
	"context"
	"errors"
	"testing"

	"go-agent-harness/internal/llm"
	"go-agent-harness/internal/memory"
	"go-agent-harness/internal/protocol"
)

type textModel struct {
	text     string
	requests []llm.Request
	err      error
}

func (m *textModel) Complete(_ context.Context, request llm.Request) (llm.Response, error) {
	m.requests = append(m.requests, request)
	return llm.Response{Message: protocol.Message{Role: protocol.RoleAssistant, Content: m.text}}, m.err
}

func TestContextModelServicesUseIndependentToolFreeRequests(t *testing.T) {
	ctx := context.Background()
	model := &textModel{text: "summary"}
	services := &modelServices{model: model}
	summary, err := services.summarizeCompactHistory(ctx, []protocol.Message{{Role: protocol.RoleUser, Content: "original"}})
	if err != nil || summary != "summary" {
		t.Fatalf("text-only summary = %q, %v", summary, err)
	}

	model.text = "[1,0]"
	indices, err := services.selectRelevantMemories(ctx, "project", []memory.CatalogItem{{Index: 0, Name: "zero"}, {Index: 1, Name: "one"}}, 1)
	if err != nil || len(indices) != 1 || indices[0] != 1 {
		t.Fatalf("selection = %v, %v", indices, err)
	}

	model.text = `[{"name":"project","type":"project","scope":"persistent","description":"stable project","body":"use Go"}]`
	records, err := services.extractMemories(ctx, "project uses Go", nil)
	if err != nil || len(records) != 1 || records[0].Scope != memory.ScopePersistent {
		t.Fatalf("extract = %#v, %v", records, err)
	}
	merged, err := services.consolidateMemories(ctx, records)
	if err != nil || len(merged) != 1 || merged[0].Name != "project" {
		t.Fatalf("consolidate = %#v, %v", merged, err)
	}
	for _, request := range model.requests {
		if len(request.Tools) != 0 || len(request.Messages) != 1 || request.MaxTokens <= 0 {
			t.Fatalf("auxiliary call inherited main Agent state: %#v", request)
		}
	}
}

func TestContextModelServicesSurfaceMissingModelAndSelectionErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := (&modelServices{}).complete(ctx, llm.Request{}); err == nil {
		t.Fatal("missing model was accepted")
	}
	model := &textModel{text: "not JSON"}
	services := &modelServices{model: model}
	if _, err := services.selectRelevantMemories(ctx, "project", []memory.CatalogItem{{Index: 0}}, 1); err == nil {
		t.Fatal("invalid selection must trigger caller fallback")
	}
	model.err = errors.New("model unavailable")
	if _, err := services.summarizeCompactHistory(ctx, nil); !errors.Is(err, model.err) {
		t.Fatalf("model error lost: %v", err)
	}
}

func TestFinalizeStoresMemoryInsideConfiguredWorkspace(t *testing.T) {
	root := t.TempDir()
	model := &textModel{text: `[{"name":"project","type":"project","scope":"persistent","description":"project language","body":"Go is the project language"}]`}
	manager := New(Config{WorkDir: root, Model: model, SystemPrompt: "system"})
	report := manager.Finalize(context.Background(), []protocol.Message{{Role: protocol.RoleUser, Content: "Remember that Go is the project language."}})
	if report.ExtractError != nil || report.Extracted != 1 {
		t.Fatalf("finalize = %#v", report)
	}
	records, err := memory.New(memory.Config{WorkDir: root}).List()
	if err != nil || len(records) != 1 || records[0].Name != "project" {
		t.Fatalf("memory not in configured directory: %#v, %v", records, err)
	}
}
