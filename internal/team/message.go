// Package team 提供 Agent Teams 的文件消息总线和类型化控制协议。
package team

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var agentNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// MessageType 区分普通协作消息、运行状态和控制协议。
type MessageType string

const (
	MessageText                 MessageType = "message"
	MessageResult               MessageType = "result"
	MessageIdleNotification     MessageType = "idle_notification"
	MessageShutdownRequest      MessageType = "shutdown_request"
	MessageShutdownResponse     MessageType = "shutdown_response"
	MessagePlanRequest          MessageType = "plan_request"
	MessagePlanApprovalRequest  MessageType = "plan_approval_request"
	MessagePlanApprovalResponse MessageType = "plan_approval_response"
)

// Metadata 保存控制消息需要的关联 ID 和审批结果。
type Metadata struct {
	RequestID string `json:"request_id,omitempty"`
	Approve   *bool  `json:"approve,omitempty"`
	Feedback  string `json:"feedback,omitempty"`
}

// Message 是写入 `.mailboxes/<agent>.jsonl` 的单条 Team 事件。
type Message struct {
	From      string      `json:"from"`
	To        string      `json:"to"`
	Content   string      `json:"content"`
	Type      MessageType `json:"type"`
	Timestamp float64     `json:"ts"`
	Metadata  Metadata    `json:"metadata"`
}

func validAgentName(name string) bool {
	return agentNamePattern.MatchString(name)
}

func validMessageType(messageType MessageType) bool {
	switch messageType {
	case MessageText, MessageResult, MessageIdleNotification,
		MessageShutdownRequest, MessageShutdownResponse,
		MessagePlanRequest, MessagePlanApprovalRequest, MessagePlanApprovalResponse:
		return true
	default:
		return false
	}
}

func validateMessage(message Message) error {
	if !validAgentName(message.From) {
		return fmt.Errorf("invalid message sender: %s", message.From)
	}
	if !validAgentName(message.To) {
		return fmt.Errorf("invalid message recipient: %s", message.To)
	}
	if !validMessageType(message.Type) {
		return fmt.Errorf("invalid message type: %s", message.Type)
	}
	if strings.TrimSpace(message.Content) == "" {
		return errors.New("message content is required")
	}
	if message.Timestamp <= 0 {
		return errors.New("message timestamp is required")
	}
	return nil
}

// FormatEvents 将 Runtime 消费的 Team 消息转换为一次模型输入。
func FormatEvents(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}
	lines := make([]string, 0, len(messages)+1)
	lines = append(lines, "[Team events]")
	for _, message := range messages {
		suffix := ""
		if message.Metadata.RequestID != "" {
			suffix = " request_id=" + message.Metadata.RequestID
		}
		lines = append(lines, fmt.Sprintf("[%s%s] %s: %s", message.Type, suffix, message.From, message.Content))
	}
	return strings.Join(lines, "\n")
}
