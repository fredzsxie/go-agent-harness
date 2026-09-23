package workflow

import (
	"context"
	"testing"
)

func noopScript(context.Context, ExecutionContext, map[string]any) (any, error) {
	return nil, nil
}

func TestRegistryValidatesAndCopiesDefinitions(t *testing.T) {
	registry := NewRegistry()
	metadata := Metadata{Name: "review-changes", Description: "Review changes", Phases: []string{"Review"}}
	if err := registry.Register(Definition{Metadata: metadata, Script: noopScript}); err != nil {
		t.Fatal(err)
	}
	metadata.Phases[0] = "changed"

	stored, ok := registry.Get("review-changes")
	if !ok || stored.Metadata.Phases[0] != "Review" {
		t.Fatalf("unexpected stored definition: %#v", stored)
	}
	stored.Metadata.Phases[0] = "mutated"
	listed := registry.List()
	if len(listed) != 1 || listed[0].Phases[0] != "Review" {
		t.Fatalf("registry exposed mutable metadata: %#v", listed)
	}
	if err := registry.Register(Definition{Metadata: metadata, Script: noopScript}); err == nil {
		t.Fatal("expected duplicate registration to fail")
	}
}

func TestRegistryRejectsInvalidDefinitions(t *testing.T) {
	tests := []Definition{
		{Metadata: Metadata{Name: "../escape", Description: "bad"}, Script: noopScript},
		{Metadata: Metadata{Name: "valid", Description: " "}, Script: noopScript},
		{Metadata: Metadata{Name: "valid", Description: "ok", Phases: []string{""}}, Script: noopScript},
		{Metadata: Metadata{Name: "valid", Description: "ok"}},
	}
	for _, definition := range tests {
		if err := NewRegistry().Register(definition); err == nil {
			t.Fatalf("expected invalid definition to fail: %#v", definition.Metadata)
		}
	}
}

func TestParseToolInputRejectsUnknownOrMalformedFields(t *testing.T) {
	parsed, err := ParseToolInput(map[string]any{
		"name": "review-changes", "args": map[string]any{"changes": "diff"}, "resume_from_run_id": "wf_review_01",
	})
	if err != nil || parsed.Args["changes"] != "diff" || parsed.ResumeFromRunID != "wf_review_01" {
		t.Fatalf("unexpected parsed input: %#v, %v", parsed, err)
	}

	invalid := []any{
		"review-changes",
		map[string]any{},
		map[string]any{"name": "review", "args": "bad"},
		map[string]any{"name": "review", "script": "inject"},
		map[string]any{"name": "review", "resume_from_run_id": ""},
	}
	for _, input := range invalid {
		if _, err := ParseToolInput(input); err == nil {
			t.Fatalf("expected invalid input to fail: %#v", input)
		}
	}
}
