package prompt

import (
	"strings"
	"testing"
)

func TestTaskInstructionsRequireTaskTools(t *testing.T) {
	builder := NewBuilder("skills", "/workspace")
	without := builder.Get(Context{EnabledTools: []string{"bash"}})
	if strings.Contains(without, "Create all task nodes first") {
		t.Fatal("task instructions should not appear without task tools")
	}
	with := builder.Get(Context{EnabledTools: []string{"update_task", "create_task"}})
	if !strings.Contains(with, "Create all task nodes first") || !strings.Contains(with, "runtime-generated IDs") {
		t.Fatalf("task instructions missing:\n%s", with)
	}
}

func TestCronInstructionsRequireScheduleTool(t *testing.T) {
	builder := NewBuilder("skills", "/workspace")
	without := builder.Get(Context{EnabledTools: []string{"bash"}})
	if strings.Contains(without, "future local time") {
		t.Fatal("cron instructions should not appear without schedule_cron")
	}
	with := builder.Get(Context{EnabledTools: []string{"schedule_cron"}})
	for _, want := range []string{"future local time", "missed times are not replayed"} {
		if !strings.Contains(with, want) {
			t.Fatalf("cron instructions missing %q:\n%s", want, with)
		}
	}
}

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

func TestTeamInstructionsRequireSpawnTool(t *testing.T) {
	builder := NewBuilder("skills", "/repo")
	without := builder.Get(Context{EnabledTools: []string{"create_task"}})
	if strings.Contains(without, "propose a small team") {
		t.Fatal("team instructions should not appear without spawn_teammate")
	}
	with := builder.Get(Context{EnabledTools: []string{"spawn_teammate", "create_worktree"}})
	for _, want := range []string{"propose a small team", "wait for the user's confirmation", "instead of polling", "Shut teammates down"} {
		if !strings.Contains(with, want) {
			t.Fatalf("team instructions missing %q:\n%s", want, with)
		}
	}
}
