package app

import "testing"

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
