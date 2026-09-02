package permission

import (
	"strings"
	"testing"
)

func TestNonInteractiveAuthorizationRejectsApprovalPrompt(t *testing.T) {
	err := AuthorizeNonInteractive("bash", map[string]any{"command": "rm temporary.txt"})
	if err == nil || !strings.Contains(err.Error(), "cannot request interactive approval") {
		t.Fatalf("expected non-interactive denial, got %v", err)
	}
	if err := AuthorizeNonInteractive("bash", map[string]any{"command": "pwd"}); err != nil {
		t.Fatalf("safe command should remain allowed: %v", err)
	}
}
