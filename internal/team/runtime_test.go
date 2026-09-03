package team

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go-agent-harness/internal/agent"
	"go-agent-harness/internal/protocol"
	"go-agent-harness/internal/task"
	"go-agent-harness/internal/worktree"
)

type channelModel struct {
	responses chan protocol.Message
	requests  chan agent.ModelRequest
}

func newChannelModel() *channelModel {
	return &channelModel{
		responses: make(chan protocol.Message, 16),
		requests:  make(chan agent.ModelRequest, 16),
	}
}

func (m *channelModel) Complete(ctx context.Context, request agent.ModelRequest) (agent.ModelResponse, error) {
	select {
	case m.requests <- request:
	case <-ctx.Done():
		return agent.ModelResponse{}, ctx.Err()
	}
	select {
	case response := <-m.responses:
		return agent.ModelResponse{Message: response}, nil
	case <-ctx.Done():
		return agent.ModelResponse{}, ctx.Err()
	}
}

func TestRuntimeKeepsTeammateAliveUntilShutdown(t *testing.T) {
	runtime, model, bus, requests, _, _ := testRuntime(t, 20*time.Millisecond)
	defer runtime.Close()
	if err := runtime.Spawn("alice", "developer", "Inspect the project.", "", false); err != nil {
		t.Fatal(err)
	}

	waitModelRequest(t, model)
	model.responses <- assistantText("Initial inspection done.")
	events := waitTeamEvents(t, bus, func(messages []Message) bool {
		return hasMessageType(messages, MessageResult) && hasMessageType(messages, MessageIdleNotification)
	})
	if len(events) < 2 {
		t.Fatalf("result and idle must be separate events: %#v", events)
	}

	if err := runtime.Send("alice", "Inspect one more file."); err != nil {
		t.Fatal(err)
	}
	request := waitModelRequest(t, model)
	if !messagesContain(request.Messages, "[Message from lead] Inspect one more file.") {
		t.Fatalf("direct message was not injected: %#v", request.Messages)
	}
	model.responses <- assistantText("Second inspection done.")
	waitTeamEvents(t, bus, func(messages []Message) bool {
		return hasMessageType(messages, MessageIdleNotification)
	})

	requestID, err := runtime.RequestShutdown("alice")
	if err != nil {
		t.Fatal(err)
	}
	shutdown := waitLeadRuntimeEvents(t, runtime, func(messages []Message) bool {
		return hasMessageType(messages, MessageShutdownResponse)
	})
	var response Message
	for _, message := range shutdown {
		if message.Type == MessageShutdownResponse {
			response = message
		}
	}
	if response.Metadata.RequestID != requestID {
		t.Fatalf("shutdown response mismatch: %#v", response)
	}
	matched, ok := requests.Get(requestID)
	if !ok || matched.Status != RequestApproved {
		t.Fatalf("shutdown protocol did not complete: %#v", matched)
	}
	waitFor(t, func() bool { return len(runtime.List()) == 0 })
}

func TestIdleMailboxHasPriorityOverTaskScan(t *testing.T) {
	runtime, model, bus, _, tasks, _ := testRuntime(t, 200*time.Millisecond)
	defer runtime.Close()
	created, err := tasks.Create("Unclaimed task", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Spawn("alice", "developer", "Wait for direction.", "", false); err != nil {
		t.Fatal(err)
	}
	waitModelRequest(t, model)
	model.responses <- assistantText("Ready.")
	waitTeamEvents(t, bus, func(messages []Message) bool {
		return hasMessageType(messages, MessageIdleNotification)
	})

	if err := runtime.Send("alice", "Handle this message first."); err != nil {
		t.Fatal(err)
	}
	request := waitModelRequest(t, model)
	if !messagesContain(request.Messages, "Handle this message first") {
		t.Fatal("mailbox message should wake teammate before task scan")
	}
	stored, err := tasks.Get(created.ID)
	if err != nil || stored.Status != task.Pending || stored.Owner != nil {
		t.Fatalf("task should remain unclaimed: %#v, %v", stored, err)
	}
}

func TestIdleTeammateAutoClaimsAndCompletesTask(t *testing.T) {
	runtime, model, bus, _, tasks, _ := testRuntime(t, 15*time.Millisecond)
	defer runtime.Close()
	created, err := tasks.Create("Run tests", "Complete the shared task.")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Spawn("bob", "tester", "Become available.", "", false); err != nil {
		t.Fatal(err)
	}
	waitModelRequest(t, model)
	model.responses <- assistantText("Available.")
	waitTeamEvents(t, bus, func(messages []Message) bool {
		return hasMessageType(messages, MessageIdleNotification)
	})

	request := waitModelRequest(t, model)
	if !messagesContain(request.Messages, "[Auto-claimed task "+created.ID+"]") {
		t.Fatalf("auto-claimed task was not injected: %#v", request.Messages)
	}
	model.responses <- assistantTool("complete-1", "complete_task", map[string]any{"task_id": created.ID})
	waitModelRequest(t, model)
	model.responses <- assistantText("All tests pass.")
	waitTeamEvents(t, bus, func(messages []Message) bool {
		return hasMessageType(messages, MessageResult) && hasMessageType(messages, MessageIdleNotification)
	})
	stored, err := tasks.Get(created.ID)
	if err != nil || stored.Status != task.Completed || stored.Owner == nil || *stored.Owner != "bob" {
		t.Fatalf("auto-claimed task was not completed: %#v, %v", stored, err)
	}
}

func TestPlanGateBlocksMutationUntilCurrentPlanApproved(t *testing.T) {
	runtime, model, bus, _, tasks, root := testRuntime(t, 20*time.Millisecond)
	defer runtime.Close()
	created, err := tasks.Create("Implement plan", "Write result.txt after approval.")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Spawn("carol", "developer", "Implement the task.", created.ID, true); err != nil {
		t.Fatal(err)
	}

	waitModelRequest(t, model)
	model.responses <- assistantTool("write-blocked", "write_file", map[string]any{"path": "result.txt", "content": "done"})
	blockedRequest := waitModelRequest(t, model)
	if !messagesContain(blockedRequest.Messages, "Blocked: plan status is required") {
		t.Fatalf("mutation should be blocked before approval: %#v", blockedRequest.Messages)
	}
	model.responses <- assistantTool("plan-1", "submit_plan", map[string]any{"plan": "1. Write result.txt\n2. Complete the task"})
	waitModelRequest(t, model)
	model.responses <- assistantText("Plan submitted.")
	planEvents := waitTeamEvents(t, bus, func(messages []Message) bool {
		return hasMessageType(messages, MessagePlanApprovalRequest)
	})
	var planID string
	for _, message := range planEvents {
		if message.Type == MessagePlanApprovalRequest {
			planID = message.Metadata.RequestID
		}
	}
	if planID == "" {
		t.Fatal("plan request ID is missing")
	}
	if err := runtime.ReviewPlan(planID, true, "Proceed."); err != nil {
		t.Fatal(err)
	}

	approvedRequest := waitModelRequest(t, model)
	if !messagesContain(approvedRequest.Messages, "[Plan approved] Proceed.") {
		t.Fatalf("approval was not delivered: %#v", approvedRequest.Messages)
	}
	model.responses <- assistantTool("write-approved", "write_file", map[string]any{"path": "result.txt", "content": "done"})
	waitModelRequest(t, model)
	if data, err := os.ReadFile(filepath.Join(root, "result.txt")); err != nil || string(data) != "done" {
		t.Fatalf("approved write did not run in task workspace: %q, %v", data, err)
	}
	model.responses <- assistantTool("complete-1", "complete_task", map[string]any{"task_id": created.ID})
	waitModelRequest(t, model)
	model.responses <- assistantText("Implementation complete.")
	waitTeamEvents(t, bus, func(messages []Message) bool {
		return hasMessageType(messages, MessageResult) && hasMessageType(messages, MessageIdleNotification)
	})

	stored, err := tasks.Get(created.ID)
	if err != nil || stored.Status != task.Completed {
		t.Fatalf("approved task was not completed: %#v, %v", stored, err)
	}
	infos := runtime.List()
	if len(infos) != 1 || infos[0].TaskID != "" || infos[0].Plan != PlanNotRequired {
		t.Fatalf("completed assignment lease was not released: %#v", infos)
	}
}

func testRuntime(t *testing.T, interval time.Duration) (*Runtime, *channelModel, *Bus, *Requests, *task.Manager, string) {
	t.Helper()
	root := t.TempDir()
	tasks := task.New(task.Config{WorkDir: root})
	bus := NewBus(BusConfig{WorkDir: root})
	requests := NewRequests()
	model := newChannelModel()
	base := agent.NewRegistry()
	for _, name := range teammateBaseTools {
		base.Register(agent.ToolSpec{Name: name}, func(context.Context, any) (string, error) {
			return "", fmt.Errorf("unbound base tool")
		})
	}
	worktrees := worktree.New(worktree.Config{WorkDir: root, Tasks: tasks})
	runtime := NewRuntime(RuntimeConfig{
		Model: model, BaseTools: base, Tasks: tasks, Worktrees: worktrees,
		Bus: bus, Requests: requests, IdleInterval: interval,
	})
	return runtime, model, bus, requests, tasks, root
}

func waitModelRequest(t *testing.T, model *channelModel) agent.ModelRequest {
	t.Helper()
	select {
	case request := <-model.requests:
		return request
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for model request")
		return agent.ModelRequest{}
	}
}

func waitTeamEvents(t *testing.T, bus *Bus, done func([]Message) bool) []Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	all := make([]Message, 0, 4)
	for {
		messages, err := bus.Wait(ctx, "lead", 50*time.Millisecond)
		if err != nil {
			t.Fatalf("wait lead inbox: %v; events=%#v", err, all)
		}
		all = append(all, messages...)
		if done(all) {
			return all
		}
	}
}

func waitLeadRuntimeEvents(t *testing.T, runtime *Runtime, done func([]Message) bool) []Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	all := make([]Message, 0, 4)
	for {
		messages, err := runtime.WaitLeadEvents(ctx, 50*time.Millisecond)
		if err != nil {
			t.Fatalf("wait runtime lead events: %v; events=%#v", err, all)
		}
		all = append(all, messages...)
		if done(all) {
			return all
		}
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied")
}

func hasMessageType(messages []Message, messageType MessageType) bool {
	for _, message := range messages {
		if message.Type == messageType {
			return true
		}
	}
	return false
}

func messagesContain(messages []protocol.Message, value string) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, value) {
			return true
		}
		for _, block := range message.Blocks {
			if strings.Contains(block.Text, value) {
				return true
			}
		}
	}
	return false
}

func assistantText(content string) protocol.Message {
	return protocol.Message{Role: protocol.RoleAssistant, Content: content, Blocks: []protocol.ContentBlock{{Type: protocol.BlockText, Text: content}}}
}

func assistantTool(id, name string, input map[string]any) protocol.Message {
	return protocol.Message{Role: protocol.RoleAssistant, Blocks: []protocol.ContentBlock{{
		Type: protocol.BlockToolUse, ToolUseID: id, ToolName: name, Input: input,
	}}}
}
