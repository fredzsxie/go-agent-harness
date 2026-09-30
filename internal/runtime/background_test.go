package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestShouldRunBackgroundRequiresExplicitBashFlag(t *testing.T) {
	if !ShouldRunBackground("bash", map[string]any{"run_in_background": true}) {
		t.Fatal("explicit background Bash call should be accepted")
	}
	for _, input := range []map[string]any{{}, {"run_in_background": false}, {"run_in_background": "true"}} {
		if ShouldRunBackground("bash", input) {
			t.Fatalf("unexpected background decision for %#v", input)
		}
	}
	if ShouldRunBackground("read_file", map[string]any{"run_in_background": true}) {
		t.Fatal("only Bash may run in the background")
	}
}

func TestManagerStartsAndCollectsCompletedTask(t *testing.T) {
	manager := NewBackground(func(_ context.Context, command string) (string, error) {
		return "finished " + command, nil
	})
	defer manager.Close()

	id, err := manager.Start("go test ./...")
	if err != nil {
		t.Fatal(err)
	}
	if id != "bg_0001" {
		t.Fatalf("unexpected id %q", id)
	}

	notification := waitForNotification(t, manager)
	for _, want := range []string{"<task_id>bg_0001</task_id>", "<status>completed</status>", "<command>go test ./...</command>", "<summary>finished go test ./...</summary>"} {
		if !strings.Contains(notification, want) {
			t.Fatalf("notification %q does not contain %q", notification, want)
		}
	}
	if collected := manager.Collect(); len(collected) != 0 {
		t.Fatalf("completed task should only be collected once: %#v", collected)
	}
}

func TestManagerReportsFailureAndEscapesNotification(t *testing.T) {
	manager := NewBackground(func(context.Context, string) (string, error) {
		return "bad <output>", errors.New("exit status 1")
	})
	defer manager.Close()

	if _, err := manager.Start("printf '<x>'"); err != nil {
		t.Fatal(err)
	}
	notification := waitForNotification(t, manager)
	for _, want := range []string{"<status>failed</status>", "printf '&lt;x&gt;'", "bad &lt;output&gt;"} {
		if !strings.Contains(notification, want) {
			t.Fatalf("notification %q does not contain %q", notification, want)
		}
	}
}

func TestManagerSignalsCompletedTask(t *testing.T) {
	release := make(chan struct{})
	completed := make(chan string, 1)
	manager := NewBackground(func(context.Context, string) (string, error) {
		<-release
		return "done", nil
	})
	defer manager.Close()
	if _, err := manager.Start("work", func(result string) { completed <- result }); err != nil {
		t.Fatal(err)
	}
	if !manager.HasRunning() {
		t.Fatal("started task should be reported as running")
	}
	close(release)

	select {
	case <-manager.Ready():
	case <-time.After(time.Second):
		t.Fatal("background completion did not signal readiness")
	}
	if manager.HasRunning() {
		t.Fatal("completed task should no longer be reported as running")
	}
	select {
	case result := <-completed:
		if result != "done" {
			t.Fatalf("unexpected completion hook result: %q", result)
		}
	default:
		t.Fatal("completion hook should run before readiness is signaled")
	}
	if !manager.HasReady() {
		t.Fatal("completed result should remain ready until collected")
	}
	if notifications := manager.Collect(); len(notifications) != 1 {
		t.Fatalf("unexpected notifications: %#v", notifications)
	}
	if manager.HasReady() {
		t.Fatal("collected result should no longer be ready")
	}
}

func waitForNotification(t *testing.T, manager *BackgroundManager) string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if notifications := manager.Collect(); len(notifications) > 0 {
			return notifications[0]
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("background task did not finish")
	return ""
}
