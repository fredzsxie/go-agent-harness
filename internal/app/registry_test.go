package app

import (
	"testing"

	agentruntime "go-agent-harness/internal/runtime"
	"go-agent-harness/loop"
)

func TestBackgroundBashOptionIsMainAgentOnly(t *testing.T) {
	mainRegistry := newDefaultRegistry()
	subRegistry := newSubagentRegistry()

	mainBash := mainRegistry.Specs()[0]
	subBash := subRegistry.Specs()[0]
	if _, ok := mainBash.Properties["run_in_background"]; !ok {
		t.Fatal("main agent Bash tool should expose run_in_background")
	}
	if _, ok := subBash.Properties["run_in_background"]; ok {
		t.Fatal("subagent Bash tool should remain synchronous")
	}
}

func TestCronToolsAreMainAgentOnly(t *testing.T) {
	mainRegistry := newDefaultRegistry()
	registerCronTools(mainRegistry, agentruntime.NewCron(agentruntime.CronConfig{WorkDir: t.TempDir()}))
	mainTools := toolNames(mainRegistry.Specs())
	subTools := toolNames(newSubagentRegistry().Specs())
	for _, name := range []string{"schedule_cron", "list_crons", "cancel_cron"} {
		if !mainTools[name] {
			t.Fatalf("main agent is missing %s", name)
		}
		if subTools[name] {
			t.Fatalf("subagent should not expose %s", name)
		}
	}
}

func toolNames(specs []loop.ToolSpec) map[string]bool {
	names := make(map[string]bool, len(specs))
	for _, spec := range specs {
		names[spec.Name] = true
	}
	return names
}
