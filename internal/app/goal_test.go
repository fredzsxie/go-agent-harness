package app

import (
	"context"
	"strings"
	"testing"

	"go-agent-harness/internal/goal"
	"go-agent-harness/internal/protocol"
)

func TestProcessInputHandlesGoalCommandsWithoutExtraSessions(t *testing.T) {
	controller, err := goal.New(goal.Config{})
	if err != nil {
		t.Fatal(err)
	}
	session := &recordingSession{totalTokens: 77}
	output := &lockedBuffer{}
	application := &App{session: session, goal: controller, out: output}

	if exit, err := application.processInput(context.Background(), "/goal go test ./... exits 0"); err != nil || exit {
		t.Fatalf("set goal failed: exit=%v err=%v", exit, err)
	}
	session.mu.Lock()
	inputs := append([]protocol.Message(nil), session.inputs...)
	session.mu.Unlock()
	if len(inputs) != 1 || inputs[0].Content != "go test ./... exits 0" {
		t.Fatalf("Goal condition was not submitted as the current task: %#v", inputs)
	}
	state, ok := controller.Active()
	if !ok || state.TokensAtStart != 77 {
		t.Fatalf("unexpected active Goal: %#v ok=%v", state, ok)
	}

	if _, err := application.processInput(context.Background(), "/goal"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Goal active: go test ./... exits 0") {
		t.Fatalf("Goal status was not printed: %q", output.String())
	}
	if _, err := application.processInput(context.Background(), "/goal off"); err != nil {
		t.Fatal(err)
	}
	if _, ok := controller.Active(); ok || !strings.Contains(output.String(), "Goal cleared") {
		t.Fatalf("Goal clear failed: %q", output.String())
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if len(session.inputs) != 1 {
		t.Fatalf("status and clear commands must not start Agent turns: %#v", session.inputs)
	}
}

func TestActiveGoalCondition(t *testing.T) {
	controller, err := goal.New(goal.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if activeGoalCondition(controller) != "" {
		t.Fatal("inactive controller returned a condition")
	}
	if _, err := controller.Set("lint passes", 0); err != nil {
		t.Fatal(err)
	}
	if got := activeGoalCondition(controller); got != "lint passes" {
		t.Fatalf("activeGoalCondition() = %q", got)
	}
}
