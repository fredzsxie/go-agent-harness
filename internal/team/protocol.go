package team

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go-agent-harness/internal/logger"
)

// RequestType 表示需要通过 request_id 匹配响应的控制协议。
type RequestType string

const (
	RequestShutdown     RequestType = "shutdown"
	RequestPlanApproval RequestType = "plan_approval"
)

// RequestStatus 表示控制请求是否仍待处理以及最终结果。
type RequestStatus string

const (
	RequestPending  RequestStatus = "pending"
	RequestApproved RequestStatus = "approved"
	RequestRejected RequestStatus = "rejected"
)

// WorkIdentity 将 Plan 审批绑定到 Teammate 当时负责的工作版本。
type WorkIdentity struct {
	Version uint64
	TaskID  string
}

// Request 保存一条 shutdown 或 plan approval 协议的关联状态。
type Request struct {
	ID           string
	Type         RequestType
	Sender       string
	Target       string
	Status       RequestStatus
	Payload      string
	WorkIdentity *WorkIdentity
	CreatedAt    time.Time
}

// Message 将请求转换为可以投递到 Target 邮箱的控制消息。
func (r Request) Message() Message {
	messageType := MessageShutdownRequest
	if r.Type == RequestPlanApproval {
		messageType = MessagePlanApprovalRequest
	}
	return Message{
		From:      r.Sender,
		To:        r.Target,
		Content:   r.Payload,
		Type:      messageType,
		Timestamp: float64(time.Now().UnixNano()) / float64(time.Second),
		Metadata:  Metadata{RequestID: r.ID},
	}
}

// Requests 管理进程内尚未完成的类型化控制请求。
type Requests struct {
	mu    sync.Mutex
	items map[string]*Request
}

func NewRequests() *Requests {
	return &Requests{items: make(map[string]*Request)}
}

// Open 创建 pending 请求；Plan 审批可携带当前 Task 的工作身份。
func (r *Requests) Open(requestType RequestType, sender, target, payload string, identity *WorkIdentity) (Request, error) {
	if requestType != RequestShutdown && requestType != RequestPlanApproval {
		return Request{}, fmt.Errorf("invalid request type: %s", requestType)
	}
	if !validAgentName(sender) || !validAgentName(target) || sender == target {
		return Request{}, errors.New("request sender and target must be different valid agent names")
	}
	if strings.TrimSpace(payload) == "" {
		return Request{}, errors.New("request payload is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id, err := r.newIDLocked()
	if err != nil {
		return Request{}, err
	}
	request := Request{
		ID:           id,
		Type:         requestType,
		Sender:       sender,
		Target:       target,
		Status:       RequestPending,
		Payload:      payload,
		WorkIdentity: cloneIdentity(identity),
		CreatedAt:    time.Now(),
	}
	r.items[id] = &request
	logger.Info("[TeamProtocol] Opened %s request %s: %s -> %s", requestType, id, sender, target)
	return cloneRequest(request), nil
}

// Match 仅接受类型、request_id、响应方和状态都匹配的首次响应。
func (r *Requests) Match(response Message) (Request, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	request, ok := r.items[response.Metadata.RequestID]
	if !ok {
		return Request{}, r.reject("unknown request_id: %s", response.Metadata.RequestID)
	}
	expected := MessageShutdownResponse
	if request.Type == RequestPlanApproval {
		expected = MessagePlanApprovalResponse
	}
	if response.Type != expected {
		return Request{}, r.reject("request %s expected %s, got %s", request.ID, expected, response.Type)
	}
	if response.From != request.Target || response.To != request.Sender {
		return Request{}, r.reject("request %s responder mismatch", request.ID)
	}
	if request.Status != RequestPending {
		return Request{}, r.reject("request %s is already %s", request.ID, request.Status)
	}
	if response.Metadata.Approve == nil {
		return Request{}, r.reject("request %s response is missing approve", request.ID)
	}
	if *response.Metadata.Approve {
		request.Status = RequestApproved
	} else {
		request.Status = RequestRejected
	}
	logger.Info("[TeamProtocol] Matched %s request %s: %s", request.Type, request.ID, request.Status)
	return cloneRequest(*request), nil
}

// Get 返回请求快照，避免调用方绕过 Match 修改协议状态。
func (r *Requests) Get(id string) (Request, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	request, ok := r.items[id]
	if !ok {
		return Request{}, false
	}
	return cloneRequest(*request), true
}

func (r *Requests) newIDLocked() (string, error) {
	for range 100 {
		data := make([]byte, 4)
		if _, err := rand.Read(data); err != nil {
			return "", err
		}
		id := "req_" + hex.EncodeToString(data)
		if _, exists := r.items[id]; !exists {
			return id, nil
		}
	}
	return "", errors.New("could not allocate a unique request id")
}

func (r *Requests) reject(format string, args ...any) error {
	err := fmt.Errorf(format, args...)
	logger.Warn("[TeamProtocol] Ignored response: %v", err)
	return err
}

func cloneRequest(request Request) Request {
	request.WorkIdentity = cloneIdentity(request.WorkIdentity)
	return request
}

func cloneIdentity(identity *WorkIdentity) *WorkIdentity {
	if identity == nil {
		return nil
	}
	copyIdentity := *identity
	return &copyIdentity
}
