package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateCronAndMatch(t *testing.T) {
	for _, expression := range []string{"* * * * *", "*/5 0,12 1-10 * 1-5", "0 9 * * 0"} {
		if err := ValidateCron(expression); err != nil {
			t.Fatalf("ValidateCron(%q): %v", expression, err)
		}
	}
	for _, expression := range []string{"* * *", "*/0 * * * *", "60 * * * *", "* * 9-2 * *", "* * * * 7"} {
		if err := ValidateCron(expression); err == nil {
			t.Fatalf("ValidateCron(%q) should fail", expression)
		}
	}

	monday := time.Date(2026, time.June, 15, 9, 10, 0, 0, time.Local)
	for _, expression := range []string{"10 9 * * 1-5", "*/5 9 15 6 0", "10 9 16 6 1"} {
		if !CronMatches(expression, monday) {
			t.Fatalf("CronMatches(%q) should match %v", expression, monday)
		}
	}
	if CronMatches("11 9 * * *", monday) {
		t.Fatal("different minute should not match")
	}
}

func TestCronDeliveryLifecycleAndMinuteDeduplication(t *testing.T) {
	manager := NewCron(CronConfig{WorkDir: t.TempDir()})
	recurring, err := manager.Schedule("30 9 * * *", "recurring work", true, false)
	if err != nil {
		t.Fatal(err)
	}
	oneShot, err := manager.Schedule("30 9 * * *", "one-shot work", false, false)
	if err != nil {
		t.Fatal(err)
	}
	moment := time.Date(2026, time.June, 15, 9, 30, 0, 0, time.Local)

	manager.Poll(moment)
	manager.Poll(moment.Add(20 * time.Second))
	delivered := manager.Consume()
	if len(delivered) != 2 {
		t.Fatalf("same minute should enqueue each job once, got %#v", delivered)
	}
	if err := manager.Acknowledge(delivered); err != nil {
		t.Fatal(err)
	}
	jobs := manager.List()
	if len(jobs) != 1 || jobs[0].ID != recurring.ID || jobs[0].PendingDelivery {
		t.Fatalf("unexpected recurring state after acknowledgement: %#v", jobs)
	}
	if jobs[0].ID == oneShot.ID {
		t.Fatal("one-shot job should be removed after acknowledgement")
	}

	manager.Poll(moment.Add(40 * time.Second))
	if manager.HasPending() {
		t.Fatal("acknowledged recurring job must not fire twice in one minute")
	}
	manager.Poll(moment.Add(24 * time.Hour))
	if !manager.HasPending() {
		t.Fatal("recurring job should fire on the next matching day")
	}
}

func TestDurableCronRestoresPendingDelivery(t *testing.T) {
	workDir := t.TempDir()
	manager := NewCron(CronConfig{WorkDir: workDir})
	job, err := manager.Schedule("0 8 * * *", "durable work", true, true)
	if err != nil {
		t.Fatal(err)
	}
	moment := time.Date(2026, time.June, 15, 8, 0, 0, 0, time.Local)
	manager.Poll(moment)

	reloaded := NewCron(CronConfig{WorkDir: workDir})
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if !reloaded.HasPending() {
		t.Fatal("pending durable job should return to the delivery queue")
	}
	delivered := reloaded.Consume()
	if len(delivered) != 1 || delivered[0].ID != job.ID {
		t.Fatalf("unexpected restored delivery: %#v", delivered)
	}
	if err := reloaded.Acknowledge(delivered); err != nil {
		t.Fatal(err)
	}

	afterAck := NewCron(CronConfig{WorkDir: workDir})
	if err := afterAck.Load(); err != nil {
		t.Fatal(err)
	}
	jobs := afterAck.List()
	if len(jobs) != 1 || jobs[0].PendingDelivery {
		t.Fatalf("acknowledgement was not persisted: %#v", jobs)
	}
	if err := afterAck.Cancel(job.ID); err != nil {
		t.Fatal(err)
	}
	final := NewCron(CronConfig{WorkDir: workDir})
	if err := final.Load(); err != nil {
		t.Fatal(err)
	}
	if len(final.List()) != 0 {
		t.Fatal("cancelled durable job should not reload")
	}
}

func TestCronLoadReportsCorruptPersistence(t *testing.T) {
	workDir := t.TempDir()
	path := filepath.Join(workDir, ".scheduled_tasks.json")
	for _, invalid := range []string{"{", "null"} {
		if err := os.WriteFile(path, []byte(invalid), 0o644); err != nil {
			t.Fatal(err)
		}
		manager := NewCron(CronConfig{WorkDir: workDir})
		if err := manager.Load(); err == nil || !strings.Contains(err.Error(), "could not load") {
			t.Fatalf("expected persistence error for %q, got %v", invalid, err)
		}
	}
}

func TestCronToolDefaultsToRecurringAndDurable(t *testing.T) {
	manager := NewCron(CronConfig{WorkDir: t.TempDir()})
	result, err := manager.RunSchedule(context.Background(), map[string]any{
		"cron": "0 9 * * *", "prompt": "run tests",
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs := manager.List()
	if len(jobs) != 1 || !jobs[0].Recurring || !jobs[0].Durable || !strings.Contains(result, jobs[0].ID) {
		t.Fatalf("unexpected scheduled job: %q, %#v", result, jobs)
	}
}

func TestCronRestoresFailedDeliveryWithoutDuplicates(t *testing.T) {
	manager := NewCron(CronConfig{WorkDir: t.TempDir()})
	if _, err := manager.Schedule("* * * * *", "retry work", false, false); err != nil {
		t.Fatal(err)
	}
	manager.Poll(time.Now())
	delivered := manager.Consume()
	manager.Restore(delivered)
	manager.Restore(delivered)
	retried := manager.Consume()
	if len(retried) != 1 || retried[0].ID != delivered[0].ID {
		t.Fatalf("restored delivery should be queued once: %#v", retried)
	}
}

func TestCronScheduleRollsBackFailedPersistence(t *testing.T) {
	manager := NewCron(CronConfig{WorkDir: t.TempDir(), Path: "."})
	if _, err := manager.Schedule("0 9 * * *", "must not remain", true, true); err == nil {
		t.Fatal("saving over a directory should fail")
	}
	if jobs := manager.List(); len(jobs) != 0 {
		t.Fatalf("failed durable schedule must roll back memory: %#v", jobs)
	}
}
