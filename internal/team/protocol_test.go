package team

import "testing"

func TestShutdownRequestMatchesOnlyExpectedResponse(t *testing.T) {
	requests := NewRequests()
	request, err := requests.Open(RequestShutdown, "lead", "alice", "Please shut down.", nil)
	if err != nil {
		t.Fatal(err)
	}
	message := request.Message()
	if message.Type != MessageShutdownRequest || message.Metadata.RequestID != request.ID {
		t.Fatalf("unexpected request message: %#v", message)
	}
	if _, err := NewBus(BusConfig{WorkDir: t.TempDir()}).SendMessage(message); err != nil {
		t.Fatalf("protocol message should be directly deliverable: %v", err)
	}
	approve := true
	response := Message{
		From: "alice", To: "lead", Type: MessageShutdownResponse,
		Metadata: Metadata{RequestID: request.ID, Approve: &approve},
	}
	matched, err := requests.Match(response)
	if err != nil || matched.Status != RequestApproved {
		t.Fatalf("match failed: %#v, %v", matched, err)
	}
	if _, err := requests.Match(response); err == nil {
		t.Fatal("duplicate response should be rejected")
	}
}

func TestRequestRejectsWrongTypeAndResponder(t *testing.T) {
	requests := NewRequests()
	request, err := requests.Open(RequestShutdown, "lead", "alice", "Stop.", nil)
	if err != nil {
		t.Fatal(err)
	}
	approve := true
	wrongType := Message{
		From: "alice", To: "lead", Type: MessagePlanApprovalResponse,
		Metadata: Metadata{RequestID: request.ID, Approve: &approve},
	}
	if _, err := requests.Match(wrongType); err == nil {
		t.Fatal("wrong response type should be rejected")
	}
	wrongResponder := Message{
		From: "bob", To: "lead", Type: MessageShutdownResponse,
		Metadata: Metadata{RequestID: request.ID, Approve: &approve},
	}
	if _, err := requests.Match(wrongResponder); err == nil {
		t.Fatal("wrong responder should be rejected")
	}
	stored, ok := requests.Get(request.ID)
	if !ok || stored.Status != RequestPending {
		t.Fatalf("invalid responses should not mutate request: %#v", stored)
	}
}

func TestPlanApprovalKeepsWorkIdentityAndRejection(t *testing.T) {
	requests := NewRequests()
	identity := &WorkIdentity{Version: 3, TaskID: "task_12345678"}
	request, err := requests.Open(RequestPlanApproval, "alice", "lead", "1. edit\n2. test", identity)
	if err != nil {
		t.Fatal(err)
	}
	identity.Version = 99
	if request.WorkIdentity == nil || request.WorkIdentity.Version != 3 {
		t.Fatalf("work identity must be copied: %#v", request.WorkIdentity)
	}
	approve := false
	matched, err := requests.Match(Message{
		From: "lead", To: "alice", Type: MessagePlanApprovalResponse,
		Metadata: Metadata{RequestID: request.ID, Approve: &approve},
	})
	if err != nil || matched.Status != RequestRejected || matched.WorkIdentity.TaskID != identity.TaskID {
		t.Fatalf("unexpected rejection: %#v, %v", matched, err)
	}
}

func TestCancelRemovesOnlyPendingRequest(t *testing.T) {
	requests := NewRequests()
	pending, err := requests.Open(RequestShutdown, "lead", "alice", "Stop.", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !requests.Cancel(pending.ID) {
		t.Fatal("pending request should be canceled")
	}
	if _, ok := requests.Get(pending.ID); ok {
		t.Fatal("canceled request should be removed")
	}
}
