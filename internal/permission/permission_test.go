package permission

import (
	"context"
	"strings"
	"testing"
)

func TestInteractionModeIsScopedToContext(t *testing.T) {
	if !IsInteractive(context.Background()) {
		t.Fatal("unconfigured user context should be interactive")
	}
	if IsInteractive(WithInteractive(context.Background(), false)) {
		t.Fatal("automatic turn context should be non-interactive")
	}
}

func TestNonInteractiveAuthorizationRejectsApprovalPrompt(t *testing.T) {
	err := AuthorizeNonInteractive("bash", map[string]any{"command": "rm temporary.txt"})
	if err == nil || !strings.Contains(err.Error(), "cannot request shell approval") {
		t.Fatalf("expected non-interactive denial, got %v", err)
	}
	if err := AuthorizeNonInteractive("bash", map[string]any{"command": "pwd"}); err == nil {
		t.Fatal("all shell commands in asynchronous turns should fail closed")
	}
}

func TestShellAuthorizationRejectsInvalidCommandWithoutPrompt(t *testing.T) {
	for _, args := range []map[string]any{{}, {"command": 1}, {"command": "  "}} {
		if err := AuthorizeNonInteractive("bash", args); err == nil || !strings.Contains(err.Error(), "non-empty string") {
			t.Fatalf("expected invalid shell command denial for %#v, got %v", args, err)
		}
	}
}

func TestExternalAuthorizationFailsClosedWithoutInteractiveInput(t *testing.T) {
	err := AuthorizeExternal("mcp__deploy__trigger", map[string]any{"service": "web"}, false)
	if err == nil || !strings.Contains(err.Error(), "non-interactive turns cannot request") {
		t.Fatalf("expected external tool denial, got %v", err)
	}
}
