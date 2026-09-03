package team

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBusPersistsAndConsumesMessages(t *testing.T) {
	root := t.TempDir()
	bus := NewBus(BusConfig{WorkDir: root})
	first, err := bus.Send("alice", "lead", "first", MessageText, Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	requestID := "req_12345678"
	if _, err := bus.Send("bob", "lead", "done", MessageResult, Metadata{RequestID: requestID}); err != nil {
		t.Fatal(err)
	}

	// 新实例验证消息来自文件，而不是只保存在当前 Bus 内存中。
	reloaded := NewBus(BusConfig{WorkDir: root})
	messages, err := reloaded.ReadInbox("lead")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Content != first.Content || messages[1].Metadata.RequestID != requestID {
		t.Fatalf("unexpected messages: %#v", messages)
	}
	if exists, err := reloaded.Peek("lead"); err != nil || exists {
		t.Fatalf("inbox should be consumed: %v, %v", exists, err)
	}
}

func TestBusConcurrentSendDoesNotLoseMessages(t *testing.T) {
	bus := NewBus(BusConfig{WorkDir: t.TempDir()})
	const total = 32
	var wait sync.WaitGroup
	for i := range total {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			if _, err := bus.Send(fmt.Sprintf("worker_%d", index), "lead", fmt.Sprintf("message-%02d", index), MessageText, Metadata{}); err != nil {
				t.Errorf("send %d: %v", index, err)
			}
		}(i)
	}
	wait.Wait()
	messages, err := bus.ReadInbox("lead")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != total {
		t.Fatalf("got %d messages, want %d", len(messages), total)
	}
	contents := make([]string, len(messages))
	for i := range messages {
		contents[i] = messages[i].Content
	}
	sort.Strings(contents)
	for i := range total {
		if contents[i] != fmt.Sprintf("message-%02d", i) {
			t.Fatalf("missing message %d: %#v", i, contents)
		}
	}
}

func TestBusWaitWakesOnMessageAndTimesOut(t *testing.T) {
	bus := NewBus(BusConfig{WorkDir: t.TempDir()})
	result := make(chan []Message, 1)
	errors := make(chan error, 1)
	go func() {
		messages, err := bus.Wait(context.Background(), "alice", time.Second)
		result <- messages
		errors <- err
	}()
	if _, err := bus.Send("lead", "alice", "start", MessageText, Metadata{}); err != nil {
		t.Fatal(err)
	}
	if messages := <-result; len(messages) != 1 || messages[0].Content != "start" {
		t.Fatalf("unexpected wake result: %#v", messages)
	}
	if err := <-errors; err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	messages, err := bus.Wait(context.Background(), "alice", 20*time.Millisecond)
	if err != nil || len(messages) != 0 || time.Since(started) < 10*time.Millisecond {
		t.Fatalf("unexpected timeout: %#v, %v", messages, err)
	}
}

func TestBusRejectsUnsafeNamesAndRetainsCorruptInbox(t *testing.T) {
	root := t.TempDir()
	bus := NewBus(BusConfig{WorkDir: root})
	if _, err := bus.Send("lead", "../outside", "unsafe", MessageText, Metadata{}); err == nil {
		t.Fatal("unsafe recipient should be rejected")
	}
	mailboxDir := filepath.Join(root, ".mailboxes")
	if err := os.MkdirAll(mailboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(mailboxDir, "lead.jsonl")
	if err := os.WriteFile(path, []byte("not-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.ReadInbox("lead"); err == nil {
		t.Fatal("corrupt inbox should fail")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("corrupt inbox should be retained: %v", err)
	}
}

func TestFormatEvents(t *testing.T) {
	formatted := FormatEvents([]Message{{
		From: "alice", Type: MessagePlanApprovalRequest, Content: "my plan",
		Metadata: Metadata{RequestID: "req_12345678"},
	}})
	if !strings.Contains(formatted, "[Team events]") ||
		!strings.Contains(formatted, "[plan_approval_request request_id=req_12345678] alice: my plan") {
		t.Fatalf("unexpected event format: %s", formatted)
	}
}
