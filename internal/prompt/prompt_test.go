package prompt

import (
	"strings"
	"testing"
)

func TestBuilderAssemblesSectionsFromRuntimeContext(t *testing.T) {
	builder := NewBuilder("- deploy", "/repo")
	prompt := builder.Get(Context{EnabledTools: []string{"write_file", "read_file"}})

	for _, want := range []string{
		"Available tools: read_file, write_file",
		"Working directory: /repo",
		"Skills available:\n- deploy",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Memories available:") {
		t.Fatalf("prompt unexpectedly included memories:\n%s", prompt)
	}
}

func TestBuilderAddsMemoryOnlyWhenPresent(t *testing.T) {
	builder := NewBuilder("", "/repo")
	withoutMemory := builder.Get(Context{})
	withMemory := builder.Get(Context{Memories: "Memories available:\n- [style](style.md) - use Go"})

	if strings.Contains(withoutMemory, "style.md") {
		t.Fatal("empty context should not load the memory section")
	}
	if !strings.Contains(withMemory, "style.md") {
		t.Fatal("non-empty context should load the memory section")
	}
}
