package app

import (
	"testing"

	"go-agent-harness/internal/mcp"
	agentruntime "go-agent-harness/internal/runtime"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/team"
	"go-agent-harness/internal/tool"
	"go-agent-harness/internal/workflow"
	"go-agent-harness/internal/worktree"
)

func TestBackgroundBashOptionIsMainAgentOnly(t *testing.T) {
	mainRegistry := newDefaultRegistry(nil)
	subRegistry := newSubagentRegistry(nil)

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
	mainRegistry := newDefaultRegistry(nil)
	base := newSubagentRegistry(nil)
	workDir := t.TempDir()
	tasks := task.New(task.Config{WorkDir: workDir})
	worktrees := worktree.New(worktree.Config{WorkDir: workDir, Tasks: tasks})
	runtime := team.NewRuntime(team.RuntimeConfig{
		BaseTools: base, Tasks: tasks, Worktrees: worktrees,
		Bus: team.NewBus(team.BusConfig{WorkDir: workDir}), Requests: team.NewRequests(),
	})
	defer runtime.Close()
	team.RegisterLeadTools(mainRegistry, runtime)
	worktree.RegisterTool(mainRegistry, worktrees)
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
	mainRegistry := newDefaultRegistry(nil)
	agentruntime.RegisterCronTools(mainRegistry, agentruntime.NewCron(agentruntime.CronConfig{WorkDir: t.TempDir()}))
	mainTools := toolNames(mainRegistry.Specs())
	subTools := toolNames(newSubagentRegistry(nil).Specs())
	for _, name := range []string{"schedule_cron", "list_crons", "cancel_cron"} {
		if !mainTools[name] {
			t.Fatalf("main agent is missing %s", name)
		}
		if subTools[name] {
			t.Fatalf("subagent should not expose %s", name)
		}
	}
}

func TestMCPToolsAreMainAgentOnly(t *testing.T) {
	mainRegistry := newDefaultRegistry(nil)
	manager := mcp.New(mainRegistry)
	mcp.RegisterTool(mainRegistry, manager)
	subRegistry := newSubagentRegistry(nil)

	if _, err := manager.Connect("docs"); err != nil {
		t.Fatal(err)
	}
	mainTools := toolNames(mainRegistry.Specs())
	subTools := toolNames(subRegistry.Specs())
	for _, name := range []string{"connect_mcp", "mcp__docs__search", "mcp__docs__get_version"} {
		if !mainTools[name] {
			t.Fatalf("main Agent is missing %s", name)
		}
		if subTools[name] {
			t.Fatalf("subagent should not expose %s", name)
		}
	}
}

func TestWorkflowToolIsMainAgentOnly(t *testing.T) {
	mainRegistry := newDefaultRegistry(nil)
	workflowRegistry := workflow.NewRegistry()
	if err := workflow.RegisterDefaults(workflowRegistry); err != nil {
		t.Fatal(err)
	}
	workflow.RegisterTool(mainRegistry, workflow.NewManager(workflow.ManagerConfig{Registry: workflowRegistry}))
	if !toolNames(mainRegistry.Specs())["workflow"] {
		t.Fatal("main agent is missing workflow")
	}
	if toolNames(newSubagentRegistry(nil).Specs())["workflow"] {
		t.Fatal("subagent should not expose workflow")
	}
}

func toolNames(specs []tool.Spec) map[string]bool {
	names := make(map[string]bool, len(specs))
	for _, spec := range specs {
		names[spec.Name] = true
	}
	return names
}
