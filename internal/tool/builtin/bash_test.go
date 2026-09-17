package builtin

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunBashCommandCapsOutput(t *testing.T) {
	output, truncated, err := runBashCommand(context.Background(), "yes x | head -c 60000", t.TempDir(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(output) != bashOutputMax {
		t.Fatalf("unexpected capped output: length=%d truncated=%v", len(output), truncated)
	}
}

func TestRunBashCommandStopsOnTimeout(t *testing.T) {
	started := time.Now()
	_, _, err := runBashCommand(context.Background(), "sleep 5", t.TempDir(), 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timed out command stopped too slowly: %s", elapsed)
	}
}
