package app

import (
	"testing"

	"go-agent-harness/internal/agent"
	agentruntime "go-agent-harness/internal/runtime"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/team"
	"go-agent-harness/internal/worktree"
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

func TestTeamToolsAreLeadOnly(t *testing.T) {
	mainRegistry := newDefaultRegistry()
	base := newSubagentRegistry()
	workDir := t.TempDir()
	tasks := task.New(task.Config{WorkDir: workDir})
	worktrees := worktree.New(worktree.Config{WorkDir: workDir, Tasks: tasks})
	runtime := team.NewRuntime(team.RuntimeConfig{
		BaseTools: base, Tasks: tasks, Worktrees: worktrees,
		Bus: team.NewBus(team.BusConfig{WorkDir: workDir}), Requests: team.NewRequests(),
	})
	defer runtime.Close()
	registerTeamTools(mainRegistry, runtime, worktrees)
	mainTools := toolNames(mainRegistry.Specs())
	subTools := toolNames(base.Specs())
	for _, name := range []string{"spawn_teammate", "list_teammates", "send_message", "request_shutdown", "request_plan", "review_plan", "create_worktree"} {
		if !mainTools[name] {
			t.Fatalf("Lead is missing %s", name)
		}
		if subTools[name] {
			t.Fatalf("subagent should not expose %s", name)
		}
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

func toolNames(specs []agent.ToolSpec) map[string]bool {
	names := make(map[string]bool, len(specs))
	for _, spec := range specs {
		names[spec.Name] = true
	}
	return names
}
