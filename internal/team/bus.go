package team

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go-agent-harness/internal/logger"
	"go-agent-harness/internal/workspace"
)

// BusConfig 定义消息文件所在的主工作区和相对目录。
type BusConfig struct {
	WorkDir    string
	MailboxDir string
}

// Bus 使用 JSONL 文件保存消息，并通过广播信号唤醒等待中的 Runtime。
type Bus struct {
	dir     string
	initErr error
	mu      sync.Mutex
	changed chan struct{}
}

// NewBus 创建文件消息总线；MailboxDir 不允许逃逸主工作区。
func NewBus(cfg BusConfig) *Bus {
	workDir := strings.TrimSpace(cfg.WorkDir)
	if workDir == "" {
		workDir = "."
	}
	mailboxDir := strings.TrimSpace(cfg.MailboxDir)
	if mailboxDir == "" {
		mailboxDir = ".mailboxes"
	}
	resolver, err := workspace.New(workDir)
	if err != nil {
		return &Bus{initErr: err, changed: make(chan struct{})}
	}
	dir, err := resolver.Resolve(mailboxDir)
	return &Bus{dir: dir, initErr: err, changed: make(chan struct{})}
}

// Send 追加一条消息并唤醒所有等待者；日志不打印正文，避免泄露协作内容。
func (b *Bus) Send(from, to, content string, messageType MessageType, metadata Metadata) (Message, error) {
	if messageType == "" {
		messageType = MessageText
	}
	message := Message{
		From:      from,
		To:        to,
		Content:   content,
		Type:      messageType,
		Timestamp: float64(time.Now().UnixNano()) / float64(time.Second),
		Metadata:  metadata,
	}
	return b.SendMessage(message)
}

// SendMessage 持久化已经组装好的类型化消息，供控制协议直接投递。
func (b *Bus) SendMessage(message Message) (Message, error) {
	if b == nil {
		return Message{}, errors.New("team bus is nil")
	}
	if err := validateMessage(message); err != nil {
		return Message{}, err
	}
	data, err := json.Marshal(message)
	if err != nil {
		return Message{}, err
	}
	data = append(data, '\n')

	b.mu.Lock()
	defer b.mu.Unlock()
	path, err := b.path(message.To)
	if err != nil {
		return Message{}, err
	}
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		return Message{}, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return Message{}, err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		logger.Error("[TeamBus] Send %s -> %s (%s): %v", message.From, message.To, message.Type, err)
		return Message{}, err
	}
	// close 会广播给全部等待者；替换 channel 供下一次消息继续等待。
	close(b.changed)
	b.changed = make(chan struct{})
	logger.Info("[TeamBus] Sent %s -> %s (%s)", message.From, message.To, message.Type)
	return message, nil
}

// ReadInbox 一次性读取并删除收件箱；解析失败时保留文件供恢复。
func (b *Bus) ReadInbox(agent string) ([]Message, error) {
	if b == nil {
		return nil, errors.New("team bus is nil")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	messages, err := b.readLocked(agent)
	if err != nil {
		logger.Error("[TeamBus] Read inbox %s: %v", agent, err)
	}
	return messages, err
}

// Peek 只检查收件箱是否存在待消费内容。
func (b *Bus) Peek(agent string) (bool, error) {
	if b == nil {
		return false, errors.New("team bus is nil")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	path, err := b.path(agent)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil && info.Size() > 0, err
}

// Wait 阻塞到消息到达、Context 取消或超时，并由唯一消费者取走当前收件箱。
func (b *Bus) Wait(ctx context.Context, agent string, timeout time.Duration) ([]Message, error) {
	if b == nil {
		return nil, errors.New("team bus is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var timer *time.Timer
	var timeoutChannel <-chan time.Time
	if timeout > 0 {
		timer = time.NewTimer(timeout)
		defer timer.Stop()
		timeoutChannel = timer.C
	}
	for {
		b.mu.Lock()
		messages, err := b.readLocked(agent)
		if err != nil || len(messages) > 0 {
			b.mu.Unlock()
			if err != nil {
				logger.Error("[TeamBus] Wait inbox %s: %v", agent, err)
			}
			return messages, err
		}
		changed := b.changed
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeoutChannel:
			return []Message{}, nil
		case <-changed:
		}
	}
}

func (b *Bus) readLocked(agent string) ([]Message, error) {
	path, err := b.path(agent)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return []Message{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	messages := make([]Message, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var message Message
		decoder := json.NewDecoder(strings.NewReader(scanner.Text()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&message); err != nil {
			return nil, fmt.Errorf("read mailbox %s: %w", agent, err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return nil, fmt.Errorf("read mailbox %s: trailing JSON content", agent)
		}
		if err := validateMessage(message); err != nil {
			return nil, fmt.Errorf("read mailbox %s: %w", agent, err)
		}
		messages = append(messages, message)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return []Message{}, nil
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	logger.Debug("[TeamBus] Consumed %d message(s) for %s", len(messages), agent)
	return messages, nil
}

func (b *Bus) path(agent string) (string, error) {
	if b == nil {
		return "", errors.New("team bus is nil")
	}
	if b.initErr != nil {
		return "", b.initErr
	}
	if !validAgentName(agent) {
		return "", fmt.Errorf("invalid mailbox recipient: %s", agent)
	}
	path := filepath.Join(b.dir, agent+".jsonl")
	if filepath.Dir(path) != b.dir {
		return "", fmt.Errorf("mailbox path escapes directory: %s", agent)
	}
	return path, nil
}
